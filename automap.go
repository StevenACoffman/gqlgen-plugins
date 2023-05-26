package gqlgen_plugins

// This file contains the Automap plugin, below.

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/99designs/gqlgen/codegen"
	"github.com/99designs/gqlgen/codegen/templates"
	"github.com/99designs/gqlgen/plugin"
	"github.com/StevenACoffman/simplerr/errors"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/StevenACoffman/gqlgen-plugins/errors/kind"
)

var PackageRoot = "github.com/Khan/webapp/"

// Automap automagically generates "mapper" functions: functions which
// convert our internal data structures (such as datastore models) into
// gqlgen's data structures.
//
// While gqlgen has some facility for doing such mapping, we want to make
// it more explicit (for general clarity, and to encourage the idea that
// the GraphQL and model interfaces may diverge significantly) and
// customize it to our needs (like ADR-303 style errors).  So we
// autogenerate "mapper" functions that clients can call to do the
// conversion.  (The name "mapper" is from ADR-312.)
//
// See @automap directive in pkg/graphql/shared-schemas/automap.graphql
type Automap struct {
	OutputDir string
}

var _incompleteMapping = errors.Wrap(kind.InvalidInput, "Not all enum values are @automapped")

var (
	_ plugin.Plugin        = Automap{}
	_ plugin.CodeGenerator = Automap{}
)

func (Automap) Name() string { return "automap" }

// AutomapError represents how we map a particular error; see
// See @automap directive for more.
type AutomapError struct {
	// From is a full package-path+name of a Go error-sentinel; we'll check if
	// the given error Is that error.  For example, this might be
	// github.com/StevenACoffman/simplerr/errors.NotFoundKind.
	From string
	// To is the GraphQL error code enum value to which we should map the given
	// error, like NOT_FOUND.
	To string
	// Log may be set to "error" or "warn", if we should log this error at that
	// level.  The default of "" says to not log.
	Log string
}

// Validate returns an error if this is not a valid mapping.
func (e AutomapError) Validate(enum ast.EnumValueList) error {
	if !strings.Contains(e.From, ".") {
		return errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid error mapping: from must be a path-qualified-name, like " +
				"github.com/StevenACoffman/simplerr/errors.NotFoundKind",
				"got": e.From})
	}
	// Not used for directive based automapped errors, but helpful with
	// determining if a default is in the enum
	if enum.ForName(e.To) == nil {
		names := make([]string, len(enum))
		for i, value := range enum {
			names[i] = value.Name
		}
		return errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid error mapping: to must be a graphql enum value.", "got": e.To, "options": names})
	}

	if e.Log != "" && e.Log != "error" && e.Log != "warn" {
		return errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid error mapping: log, if set, must be 'error' or 'warn'.", "got": e.Log})
	}

	return nil
}

// PkgPath returns the package-path of the error.
func (e AutomapError) PkgPath() string {
	i := strings.LastIndex(e.From, ".") // guaranteed to be != -1 by Validate
	return e.From[:i]
}

// Name returns the unqualified-name of the error.
func (e AutomapError) Name() string {
	i := strings.LastIndex(e.From, ".") // guaranteed to be != -1 by Validate
	return e.From[i+1:]
}

// _automapTemplateData is the object we pass to automap.gotpl.
type _automapTemplateData struct {
	// the mappers to generate
	Mappers []*_automapper
	// information about any mappers we couldn't generate (but that were not
	// explicitly requested); we'll include this in comments.
	Errors []string
}

// _automapper is the configuration for each automapper we will
// generate; we pass a []*_automapper to the template.
//
// For the fields below, consider a mutation with the following schema:
//
//	type Mutation { myMutation: MyMutation }
//	type MyMutation { error: MyMutationError, user: User }
//	type MyMutationError { code: MyMutationErrorCode!, debugMessage: String! }
//	enum MyMutationErrorCode { UNAUTHORIZED, NOT_FOUND, INTERNAL }
type _automapper struct {
	// MapperName is the name of the automapper function we should generate.
	// In the above example, this would be "MyMutationErr".
	MapperName string
	// GraphQLTypeName is the name of the type we will return, in GraphQL.
	// (This is just used in documentation.)  In the above example it would be
	// "MyMutation".
	GraphQLTypeName string
	// GraphQLModel, GraphQLError, and GraphQLErrorCode are the Go types to
	// which we are mapping, for the whole model, the error field, and the
	// error-code field, respectively.  Actually, the first two are the
	// struct-types; the models-values are in fact pointers to those but that
	// is not represented in this type, to save unwrapping and rewarapping.  In
	// the above example, these would be `graphql.MyMutation`,
	// `graphql.MyMutationError`, and `graphql.MyMutationErrorCode`.
	// TODO(benkraft): Handle any cases that come up where they aren't pointers
	// (e.g. can error be a slice or not a pointer? can code be optional?)
	GraphQLModel, GraphQLError, GraphQLErrorCode types.Type
	// ErrorField and ErrorCodeField are the Go names of the error and
	// error field of GraphQLModel and the error-code and debug-message fields
	// of GraphQLError respectively.  (They have types GraphQLError,
	// GraphQLErrorCode, and [*]string respectively.)  DebugMessageField may be
	// "" if there is no such field.
	// In the above example, these would be "Error", "Code", and
	// "DebugMessage".
	ErrorField, ErrorCodeField, DebugMessageField string
	// Errors provides information about which errors we map to what, in order
	// of precedence.
	Errors []AutomapError
	// DefaultCode is the code (typically "INTERNAL") to which we will match
	// all non-nil errors, or "" if there is no such code, in which case we
	// will map them to the GraphQL errors array (i.e. `return nil, err`) as a
	// fallback.
	DefaultCode string
	// DebugMessageIsPointer is set if the debug-message field has type
	// *string rather than string.  (In the above example it would be false,
	// because debugMessage is required in the schema.)
	DebugMessageIsPointer bool
}

// _defaultErrorMappings are the default error codes we'll map
// each error-kind to, if the error code exists.  Modified from
// web.response.errors.GeneralApplicationErrorCode in Python; we
// can add to this if other enum-values become common.
var _defaultErrorMappings = []AutomapError{
	{
		From: "github.com/StevenACoffman/simplerr/errors.NotFoundKind",
		To:   "NOT_FOUND",
		Log:  "warn",
	},
	{
		From: "github.com/StevenACoffman/simplerr/errors.InvalidInputKind",
		To:   "INVALID_INPUT",
		Log:  "warn",
	},
	// also common (we'll include whichever matches the enum)
	{
		From: "github.com/StevenACoffman/simplerr/errors.InvalidInputKind",
		To:   "INVALID",
		Log:  "warn",
	},
	{
		From: "github.com/StevenACoffman/simplerr/errors.NotAllowedKind",
		To:   "NOT_ALLOWED",
		Log:  "warn",
	},
	{
		From: "github.com/StevenACoffman/simplerr/errors.UnauthorizedKind",
		To:   "UNAUTHORIZED",
		Log:  "warn",
	},
	{
		From: "github.com/StevenACoffman/simplerr/errors.NotImplementedKind",
		To:   "NOT_IMPLEMENTED",
		Log:  "",
	},
	// Internal is not included here since it's the default for all unmatched
	// errors.
	// TODO(benkraft): Add a standard sentinel for too many requests (perhaps
	// in pkg/web/ratelimit).
}

// _findField returns the field of the given object with the given name in Go,
// if any.
func _findField(obj *codegen.Object, goName string) *codegen.Field {
	for _, f := range obj.Fields {
		if f.GoFieldName == goName {
			return f
		}
	}
	return nil
}

func _safelyCastToString(val any) string {
	return fmt.Sprintf("%v", val)
}

func _getListArgumentFromDirective(directive *ast.Directive, arg string) ([]string, error) {
	value := directive.Arguments.ForName(arg)
	if value == nil {
		return nil, nil
	}
	argument, err := value.Value.Value(nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Some arguments that are []string in the schema are actually
	// implemented as string because of Input Coercion. Since the gqlgen
	// isn't smart enough to know this they come out as just a string, so
	// we must adapt them to []string. See:
	// https://spec.graphql.org/June2018/#sec-Type-System.List

	var slice []any
	var ok bool
	if slice, ok = argument.([]any); !ok {
		return []string{_safelyCastToString(argument)}, nil
	}
	result := make([]string, len(slice))
	for i := range slice {
		result[i] = _safelyCastToString(slice[i])
	}
	return result, nil
}

func _getArgumentFromDirective(directive *ast.Directive, arg string) string {
	value := directive.Arguments.ForName(arg)
	if value == nil {
		return ""
	}
	return value.Value.Raw
}

// Convert a relpath to be a go-style package name.  The relpath is
// taken to be relative to the directory that `obj` lives in.
func _relpathToPackage(obj *codegen.Object, relpath string) (string, error) {
	// Where the object lives is a relative path.  gqlparser doesn't
	// say, but mI assume it's relative to the gqlgen.yml file, which
	// I think has to be in the current directory when running gqlgen.
	objAbspath, err := filepath.Abs(obj.Definition.Position.Src.Name)
	if err != nil {
		return "", errors.WithStack(err)
	}

	abspath := filepath.Clean(filepath.Join(filepath.Dir(objAbspath), relpath))
	dotIndex := strings.LastIndex(abspath, ".")
	if strings.Contains(abspath[dotIndex+1:], "/") {
		return "", errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid package-path: should be ./path.Symbol", "path": abspath})
	}
	pkgAbspath := abspath[:dotIndex]
	if strings.HasSuffix(pkgAbspath, "/") {
		return "", errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid package-path: should be ./path.Symbol",
				"path": pkgAbspath})
	}
	// Check that the path is a valid package.
	stat, err := os.Stat(pkgAbspath)
	if err != nil {
		return "", errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid package-path: nonexistent directory", "path": pkgAbspath, "originErr": err})
	}
	if !stat.IsDir() {
		return "", errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "invalid package-path: not a directory",
				"path": pkgAbspath})
	}

	currWd, err := os.Getwd()
	if err != nil {
		return "", errors.Wrap(kind.Internal, "unable to get working directory")
	}

	path := filepath.Join(currWd, abspath)

	return PackageRoot + path, nil
}

// _getAutomapData returns the template data needed to generate the automapper
// for the given type, or returns an error if there was a problem doing so.
//
// This is the bulk of the work of this plugin!
//
// Note that a codegen.Object is gqlgen's representation of a GraphQL "object
// type", which is the normal kind of GraphQL type (not an input, interface,
// enum, etc.) and the primary one we deal with in this file.
//
// Arguments:
//
//	obj is the type for which we are generating an automapper
//	objects is the map of GraphQL type-name to object, for all object types
func _getAutomapData(
	obj *codegen.Object,
	objects map[string]*codegen.Object,
) (*_automapper, error) {
	// TODO(benkraft): Allow configuring the field-name we look for, if
	// we ever need it. (Same for "Code", below.)
	errorField := _findField(obj, "Error")
	if errorField == nil {
		// If the object doesn't have an Error field, we can safely ignore it
		return nil, nil
	}

	errorObj := objects[errorField.FieldDefinition.Type.Name()]
	if errorObj == nil {
		// error is not a GraphQL object (maybe a string).
		return nil, errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "error field was not a valid object type",
				"got": errorField.FieldDefinition.Type.Name()})
	}

	codeField := _findField(errorObj, "Code")
	if codeField == nil {
		return nil, errors.Wrap(kind.InvalidInput, "no error-code field found")
	}

	if codeField.TypeReference.Definition.Kind != ast.Enum {
		return nil, errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "error field was not an enum type",
				"got": codeField.TypeReference.Definition.Kind})
	}
	enumValues := codeField.TypeReference.Definition.EnumValues

	// Second, build the template data.
	var templateData _automapper

	// mapper name is [automap.]<GoTypeName>Err
	unqualified := func(*types.Package) string { return "" }
	goTypeName := types.TypeString(obj.Type, unqualified)
	templateData.MapperName = goTypeName + "Err"
	templateData.GraphQLTypeName = obj.Definition.Name

	// TODO(benkraft): somewhere we should perhaps validate that these
	// types "look right", e.g. that we don't have a []*MyMutationError
	// instead of a *MyMutationError.  (If that happens the generated
	// code will not compile.)  In practice it doesn't seem to come up
	// when our other conditions are met.
	templateData.GraphQLModel = obj.Type
	templateData.GraphQLError = errorObj.Type
	templateData.GraphQLErrorCode = codeField.TypeReference.Target

	templateData.ErrorField = errorField.GoFieldName
	templateData.ErrorCodeField = codeField.GoFieldName

	// Build the error mappings using automap directives
	handledEnumValues := map[string]bool{}
	for _, e := range enumValues {
		automapDirective := e.Directives.ForName("automap")
		if automapDirective != nil {
			// Typestring is something like
			// "github.com/StevenACoffman/simplerr/errors.NotFoundKind"
			// or "../../pkg/lib/errors.NotFoundKind"
			typeStrings, err := _getListArgumentFromDirective(automapDirective, "go")
			if err != nil {
				return nil, err
			}
			for _, typeString := range typeStrings {
				if typeString == "" {
					continue
				}
				// Take it to be relative the directory of the .graphql
				// file if typeString is a relative path
				// (starts with ./ or ../)
				if strings.HasPrefix(typeString, "./") ||
					strings.HasPrefix(typeString, "../") {
					var err error
					typeString, err = _relpathToPackage(obj, typeString)
					if err != nil {
						return nil, err
					}
				}

				automapError := AutomapError{
					From: typeString,
					To:   e.Name,
					// TODO(jeremygervais) handle the case where only the
					// log is present like: UNAUTHORIZED @automap(logLevel:
					// "warn")
					Log: _getArgumentFromDirective(automapDirective, "log"),
				}
				err := automapError.Validate(enumValues)
				if err != nil {
					return nil, err
				}
				templateData.Errors = append(templateData.Errors, automapError)
			}
			handledEnumValues[e.Name] = true
		}
	}

	for _, e := range _defaultErrorMappings {
		// TODO(benkraft): Omit any default mappings that have the same From
		// as a configured mapping (they will generate duplicate cases, which
		// are dead code).  This can happen if you wanted to change a standard
		// error-kind to map to a nonstandard code, or make it log.
		if e.Validate(enumValues) == nil {
			templateData.Errors = append(templateData.Errors, e)
			handledEnumValues[e.To] = true
		} // it's fine if these don't exist.
	}

	switch {
	case enumValues.ForName("INTERNAL") != nil:
		templateData.DefaultCode = "INTERNAL"
		handledEnumValues["INTERNAL"] = true
	case enumValues.ForName("INTERNAL_ERROR") != nil:
		templateData.DefaultCode = "INTERNAL_ERROR"
		handledEnumValues["INTERNAL_ERROR"] = true
	case enumValues.ForName("UNEXPECTED_ERROR") != nil:
		templateData.DefaultCode = "UNEXPECTED_ERROR"
		handledEnumValues["UNEXPECTED_ERROR"] = true
	}

	if len(handledEnumValues) < len(enumValues) {
		missingEnums := make([]string, 0)
		for _, e := range enumValues {
			if _, ok := handledEnumValues[e.Name]; !ok {
				missingEnums = append(missingEnums, e.Name)
			}
		}
		// Not all enum values in this enum are mapped either explicitly or by
		// default, soe want to raise this as an error and refuse to generate.
		// The error will appear in generated/autogen/autogen.go for
		// visibility.
		return nil, errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "Not all values automapped",
				"obj": obj.Name, "missing": missingEnums})
	}

	debugMessageField := _findField(errorObj, "DebugMessage")
	if debugMessageField != nil {
		switch debugMessageField.TypeReference.GO.String() {
		case "string":
			templateData.DebugMessageField = debugMessageField.GoFieldName
		case "*string":
			templateData.DebugMessageField = debugMessageField.GoFieldName
			templateData.DebugMessageIsPointer = true
		default:
			// some other type we don't know how to generate
		}
	}

	return &templateData, nil
}

func _sortAutoMapForSwitchOrder(mappers []*_automapper) {
	for _, _automapper := range mappers {
		automapper := _automapper
		sort.SliceStable(automapper.Errors, func(i, j int) bool {
			iFrom := automapper.Errors[i].From
			jFrom := automapper.Errors[j].From
			// For the sake of simplicity in producing a stable sort, we sort
			// errors alphabetically with 2 groups, pkg and not pkg where pkg
			// errors are last.
			iIsPkg := strings.HasPrefix(iFrom, "github.com/StevenACoffman/simplerr/errors.")
			jIsPkg := strings.HasPrefix(jFrom, "github.com/StevenACoffman/simplerr/errors.")
			switch {
			case iIsPkg == jIsPkg:
				// either both are in pkg/lib or both are not. In that case
				// both i and j are in the same group and we can just sort them
				// alpha.
				return i < j
			case iIsPkg:
				// only i is in pkg/lib, so we want it to go last
				return false
			default:
				// only j is in pkg, so we want it to go first
				return true
			}
		})
	}
}

// GenerateCode is gqlgen's entrypoint to the plugin, and as the name
// suggests, generates the automapping code.
func (p Automap) GenerateCode(cfg *codegen.Data) error {
	var templateData _automapTemplateData

	// Build a map of name -> object, to make those lookups faster.
	objects := map[string]*codegen.Object{}
	for _, obj := range cfg.Objects {
		objects[obj.Definition.Name] = obj
	}

	// Now actually go through the objects, and build the automappers.
	for _, obj := range cfg.Objects {
		automapper, err := _getAutomapData(obj, objects)
		switch {
		case errors.Is(err, _incompleteMapping):
			return err
		case err != nil:
			templateData.Errors = append(templateData.Errors,
				strings.ReplaceAll( // strip newlines
					fmt.Sprintf("%v: %v", obj.Definition.Name, err.Error()),
					"\n", " "))
		case automapper != nil:
			templateData.Mappers = append(templateData.Mappers, automapper)
		}
	}

	// We want errors in each mapper to be sorted such that pkg errors go last
	// in the switch case statement. This is to
	// avoid the case where the graphql schema has 2 automap'd errors like:
	// SOME_GENERIC_ERROR @automap(go: "../../pkg/lib/errors.NotFoundKind)
	// SOME_SPECIFIC_ERROR @automap(go: "./mutation.UserNotFoundError")
	// In the above case, if mutation.UserNotFound is a NotFoundKind, the
	// switch case would produce a case for NotFoundKind before
	// UserNotFoundError which would make the later unreachable.
	_sortAutoMapForSwitchOrder(templateData.Mappers)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{"message": "unable to determine caller file location to find template"})
	}
	templateFilename := filepath.Join(filepath.Dir(thisFile), "automap.gotpl")
	templateBytes, err := os.ReadFile(templateFilename)
	if err != nil {
		return errors.WithStack(err)
	}

	// Finally, render the template, using gqlgen's helpers.
	err = templates.Render(templates.Options{
		// TODO(benkraft): Allow configuring these.
		PackageName: "automap",
		Filename:    filepath.Join(p.OutputDir, "automap.go"),

		PackageDoc: "// Package automap defines autogenerated utilities for converting\n" +
			"// internal model types to GraphQL types.",
		GeneratedHeader: true, // include "DO NOT EDIT" line

		Template: string(templateBytes),
		Data:     &templateData,
		Packages: cfg.Config.Packages,
	})
	return errors.WithStack(err)
}
