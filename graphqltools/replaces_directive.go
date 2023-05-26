package graphqltools

// This file contains tooling for processing the @replaces directive in a
// GraphQL schema. See GetReplacesDirectiveUpdates for details.
//
// Some conventions used in this file: consider the directive:
//    type AwesomelyNamedType @replaces(name: "TerriblyNamedType) {
//        kaid: String
//        awesomelyNamedField: AnotherGreatType @replaces(
//          name: "terriblyNamedField", type: "AnotherNotSoGreatType")
//    }
// In the code below, we will use oldName to refer to the name being replaced
// ("TerriblyNamedType", "terriblyNamedField"), oldType to refer to any types
// being replaced ("AnotherNotSoGreatType"), and newName/newType to refer
// to their replacements.
//
// Note this code is only interested in emitting *old* names and types.  The
// new names and types are already in the schema files (with `@replaces`
// directives) and are working just fine as they are.

import (
	"fmt"
	"github.com/StevenACoffman/gqlgen-plugins/errors/kind"
	"regexp"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"

	"github.com/StevenACoffman/simplerr/errors"
)

type ReplaceInfo struct {
	OldName                 string
	OldTypeName             string
	WasRequiredBeforeRename bool
	TreatZeroAsUnset        bool
	TreatZeroAsUnsetPresent bool
}

func GetReplaceInfo(directives ast.DirectiveList) (*ReplaceInfo, error) {
	directive := directives.ForName("replaces")

	if directive == nil {
		return nil, errors.WithStack(kind.NotFound)
	}

	arg := directive.Arguments.ForName("name")

	if arg == nil {
		// The schema validator should enforce this is present.
		return nil, errors.Wrap(kind.Internal, "name required on @replaces directive")
	}

	replaceInfo := &ReplaceInfo{OldName: arg.Value.Raw}

	if arg = directive.Arguments.ForName("type"); arg != nil {
		replaceInfo.OldTypeName = arg.Value.Raw
	}

	if arg = directive.Arguments.ForName("wasRequiredBeforeRename"); arg != nil {
		replaceInfo.WasRequiredBeforeRename = arg.Value.Raw == "true"
	}

	if arg = directive.Arguments.ForName("treatZeroAsUnset"); arg != nil {
		replaceInfo.TreatZeroAsUnset = arg.Value.Raw == "true"
		replaceInfo.TreatZeroAsUnsetPresent = true
	}

	return replaceInfo, nil
}

type ErrorList []error

func (e ErrorList) Error() string {
	messages := make([]string, len(e))
	for i := range e {
		messages[i] = e[i].Error()
	}
	return strings.Join(messages, "\n")
}

// Replacer holds information about renames in a schema. Call
// GetReplacesDirectiveUpdates to processes a schema. See that method for more
// information.
type Replacer struct {
	// Errors collected while performing renames. Returned by
	// GetReplacesDirectiveUpdates after all @replaces directives have been
	// processed.
	errors ErrorList

	// A map from (new) object name to fields being renamed on that object.
	fields map[string][]_fieldInfo
	// All the top-level definitions with names being renamed. A top-level
	// definition is an object, input object, interface, union or enum.
	definitions []_definitionInfo
	// A map from (new) enum name to enum values being renamed on the enum.
	enumValues map[string][]_enumValueInfo
	// A map from (new) object name to additional interfaces that need to be
	// implemented by the object (because the object implements a renamed
	// interface).
	extraImplements map[string][]string
	// A map from (new) union name to additional union members that need to be
	// included in the union (because the union includes a renamed union
	// member).
	extraUnionMembers map[string][]string

	// A map from new type names to old type names, for names being renamed.
	// Includes all renamed definition names.
	cacheReplacedTypes map[string]string

	// A map from (new) definition names to definition kinds.
	definitionKinds map[string]ast.DefinitionKind

	// A map from (new) object names to the keys on those objects. The keys are
	// strings as they appear in the "fields" argument of the @key directive,
	// e.g. "kaid classroomId" or "course { id }".
	federationKeys map[string][]string

	// Set if the replacer has already processed a schema.
	hasProcessedSchema bool
}

func NewReplacer() *Replacer {
	return &Replacer{
		fields:             make(map[string][]_fieldInfo),
		enumValues:         make(map[string][]_enumValueInfo),
		extraImplements:    make(map[string][]string),
		extraUnionMembers:  make(map[string][]string),
		cacheReplacedTypes: make(map[string]string),
		definitionKinds:    make(map[string]ast.DefinitionKind),
		federationKeys:     make(map[string][]string),
	}
}

type _definitionInfo struct {
	definition *ast.Definition
	oldName    string
}

type _fieldInfo struct {
	field       *ast.FieldDefinition
	oldName     string
	oldTypeName string
}

type _enumValueInfo struct {
	enumValue *ast.EnumValueDefinition
	newName   string
	oldName   string
}

// ValidateReplacesDirectives returns an error if any @replaces directive uses
// in the given schema are invalid.
func ValidateReplacesDirectives(schema *ast.Schema) error {
	replacer := NewReplacer()

	replacer.processSchema(schema)

	if len(replacer.errors) > 0 {
		return errors.WrapWithFields(kind.InvalidInput, errors.Fields{"errorlist": replacer.errors})
	}

	return nil
}

// GetReplacesDirectiveUpdates applies any @replaces directives found in the
// given schema. It returns a schema that should be included along with the
// original schema to perform the @replaces updates.
func GetReplacesDirectiveUpdates(schema *ast.Schema) (string, error) {
	replacer := NewReplacer()

	replacer.processSchema(schema)
	additions := replacer.getSchemaAdditions()

	if len(replacer.errors) > 0 {
		return "", errors.WrapWithFields(kind.InvalidInput, errors.Fields{"errorlist": replacer.errors})
	}

	return additions, nil
}

// processSchema records metadata about uses of @replaces directives in the
// given schema.
func (r *Replacer) processSchema(schema *ast.Schema) {
	if r.hasProcessedSchema {
		r.errors = append(r.errors, errors.Wrap(kind.Internal, "processSchema called multiple times"))
		return
	} else {
		r.hasProcessedSchema = true
	}

	for _, definition := range schema.Types {
		r._processDefinition(definition)

		switch definition.Kind {
		case ast.Object, ast.InputObject, ast.Interface:
			for _, field := range definition.Fields {
				r._processField(definition.Name, definition.Kind, field)
			}
		case ast.Enum:
			for _, enumValue := range definition.EnumValues {
				r._processEnumValue(definition.Name, enumValue)
			}
		}
	}

	// Go through the types again to find any objects that implement renamed
	// interfaces or unions that included renamed union members. These types
	// will be updated (via the extend keyword) to implement/include the old
	// type names.
	for _, definition := range schema.Types {
		switch definition.Kind {
		case ast.Object:
			for _, iface := range definition.Interfaces {
				r._processInterfaceImplementation(definition.Name, iface)
			}
		case ast.Union:
			for _, memberName := range definition.Types {
				r._processUnionMember(definition.Name, memberName)
			}
		}
	}
}

func (r *Replacer) getReplaceInfo(directives ast.DirectiveList) (*ReplaceInfo, bool) {
	replaceInfo, err := GetReplaceInfo(directives)
	if errors.Is(err, kind.NotFound) {
		return nil, false
	}
	if err != nil {
		r.errors = append(r.errors, err)
		return nil, false
	}
	return replaceInfo, true
}

func (r *Replacer) _processField(
	typeName string,
	definitionKind ast.DefinitionKind,
	field *ast.FieldDefinition,
) {
	replaceInfo, ok := r.getReplaceInfo(field.Directives)
	if !ok {
		// Verify that none of the arguments are renamed. While it would be
		// possible to allow argument renames by including both the old and
		// new names as nullable arguments (and enforcing that only one is
		// set, and mapping between the two), requiring the field to be also
		// renamed is simpler to reason about: we don't have a deal with
		// enforcing non-nullability in mapping code. Callers need to be
		// updated to use the new argument anyway, so also updating the field
		// name isn't much more of a change.
		for _, arg := range field.Arguments {
			if _, ok := r.getReplaceInfo(arg.Directives); ok {
				r.errors = append(r.errors,
					errors.WrapWithFields(kind.Internal,
						errors.Fields{
							"message":  "@replaces directive on arguments can only be used on renamed fields",
							"type":     typeName,
							"field":    field.Name,
							"argument": arg.Name,
						},
					),
				)
			}
		}
		return
	}

	if definitionKind == ast.InputObject {
		if field.Type.NonNull {
			r.errors = append(r.errors, errors.WrapWithFields(kind.InvalidInput,
				errors.Fields{
					"message": "input fields using the @replaces directive must be nullable",
					"type":    typeName,
					"field":   field.Name,
				},
			))
		}
		if _isNonListField(field) && !replaceInfo.TreatZeroAsUnsetPresent {
			r.errors = append(r.errors, errors.WrapWithFields(kind.InvalidInput,
				errors.Fields{
					"message": "@replaces directive on non-list input fields must include treatZeroAsUnset:true or treatZeroAsUnset:false",
					"type":    typeName,
					"field":   field.Name,
				},
			))
		}
	}

	r.fields[typeName] = append(r.fields[typeName], _fieldInfo{
		field:       field,
		oldName:     replaceInfo.OldName,
		oldTypeName: replaceInfo.OldTypeName,
	})
}

// _isNonListField returns whether the give field has a non-list type, e.g.
// String or User! vs. [String] or [User!]!.
//
// A gqlparser Type looks like:
//
//	Type{NamedType: string, Elem *Type, NonNull: bool}
//
// Exactly one of NamedType and Elem is zero. A non-list type has a non-zero
// NamedType and zero Elem. A list type has a zero NamedType and a non-zero
// Elem.
//
// Here are some examples:
//
//	String    => Type{NamedType: "String", Elem: nil, NonNull: false} // <1>
//	String!   => Type{NamedType: "String", Elem: nil, NonNull: true}
//	[String]  => Type{NamedType: "", Elem: <1>, NonNull: false}
//	[String]! => Type{NamedType: "", Elem: <1>, NonNull: true}
//	User      => Type{NamedType: "User", Elem: nil, NonNull: false}
func _isNonListField(field *ast.FieldDefinition) bool {
	return field.Type.NamedType != ""
}

func (r *Replacer) _processEnumValue(enumName string, enumValue *ast.EnumValueDefinition) {
	replaceInfo, ok := r.getReplaceInfo(enumValue.Directives)
	if !ok {
		return
	}

	if replaceInfo.OldTypeName != "" {
		r.errors = append(r.errors, errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{
				"message": "@replaces directive on enum values can only use `name` argument",
				"enum":    enumName, "enumValue": enumValue.Name},
		))
	}

	r.enumValues[enumName] = append(r.enumValues[enumName], _enumValueInfo{
		enumValue: enumValue,
		newName:   enumValue.Name,
		oldName:   replaceInfo.OldName,
	})
}

func (r *Replacer) _processDefinition(def *ast.Definition) {
	r.definitionKinds[def.Name] = def.Kind
	r.federationKeys[def.Name] = _getFederationKeys(def)

	replaceInfo, ok := r.getReplaceInfo(def.Directives)
	if !ok {
		return
	}

	if replaceInfo.OldTypeName != "" {
		r.errors = append(r.errors, errors.WrapWithFields(kind.InvalidInput,
			errors.Fields{
				"message":    "@replaces directive on definitions can only use `name` argument",
				"definition": def.Name},
		))
	}

	r.definitions = append(
		r.definitions, _definitionInfo{definition: def, oldName: replaceInfo.OldName})

	r.cacheReplacedTypes[def.Name] = replaceInfo.OldName
}

func _getFederationKeys(def *ast.Definition) []string {
	var keys []string
	for _, directive := range def.Directives {
		if directive.Name == "key" {
			for _, arg := range directive.Arguments {
				if arg.Name == "fields" {
					keys = append(keys, arg.Value.Raw)
				}
			}
		}
	}
	return keys
}

func (r *Replacer) _processInterfaceImplementation(objectName string, interfaceName string) {
	// Look for interface names that have been renamed.
	oldName, ok := r.cacheReplacedTypes[interfaceName]
	if !ok {
		return
	}
	r.extraImplements[objectName] = append(r.extraImplements[objectName], oldName)
}

func (r *Replacer) _processUnionMember(unionName string, memberName string) {
	// Look for union members that have been renamed.
	oldName, ok := r.cacheReplacedTypes[memberName]
	if !ok {
		return
	}
	r.extraUnionMembers[unionName] = append(r.extraUnionMembers[unionName], oldName)
}

type _internalFormatter interface {
	// FormatDefinition serializes the given definition AST to the formatter's
	// output buffer. When `extend` is true, the definition is prefixed with
	// the "extend" keyword, e.g. `extend type Classroom { id: ID! }`.
	FormatDefinition(definition *ast.Definition, extend bool)
}

// getSchemaAdditions returns a schema containing deprecated types and fields;
// the content is meant to be placed in a deprecated.graphql file alongside a
// service's other schema files. Note that the input schema contains all the
// new names already; the additional schema contains old types and fields
// (added via type extensions, when appropriate) that are needed to maintain
// backward compatibility with the version of the schema that existed before
// the types and fields were renamed.
func (r *Replacer) getSchemaAdditions() string {
	if !r.hasProcessedSchema {
		r.errors = append(
			r.errors, errors.Wrap(kind.Internal, "must call processSchema before getSchemaAdditions"))
		return ""
	}

	var buf strings.Builder
	f, ok := formatter.NewFormatter(&buf).(_internalFormatter)
	if !ok {
		panic("the gqlgen formatter API must have changed; update this code")
	}

	sort.Slice(r.definitions, func(i, j int) bool {
		return r.definitions[i].oldName < r.definitions[j].oldName
	})

	// Definition updates. Definitions cover objects, input objects,
	// interfaces, unions and enums.
	for _, definitionInfo := range r.definitions {
		hasExtend := _definitionHasExtends(definitionInfo.definition)
		oldDefinition := *definitionInfo.definition
		deprecatedMessage := fmt.Sprintf(
			"Deprecated: Replaced by %s.", definitionInfo.definition.Name)
		if oldDefinition.Description == "" {
			// TODO(marksandstrom) Emit the above description as a comment when
			// the "extend" keyword is present.
			if !hasExtend {
				oldDefinition.Description = deprecatedMessage
			}
		} else {
			oldDefinition.Description = oldDefinition.Description + "\n" + deprecatedMessage
		}
		oldDefinition.Name = definitionInfo.oldName
		oldDefinition.Directives = _removeReplacesDirective(oldDefinition.Directives)
		oldDefinition.Fields = make(
			ast.FieldList, len(definitionInfo.definition.Fields))
		// Clear @replaces directives on fields.
		//
		// These fields are the new field names, which means that we emit new
		// field names for types that have been renamed. It would be possible
		// to only emit new fields in new types and old fields in old types,
		// but we emit both the new and old fields for both the new and old
		// types because it's easier to reason about: mapping code doesn't
		// need to be concerned if it's dealing with a new or old type; all
		// the fields match up.
		for i, field := range definitionInfo.definition.Fields {
			newField := *field
			newField.Directives = _removeReplacesDirective(newField.Directives)
			oldDefinition.Fields[i] = &newField

			newField.Arguments = make(ast.ArgumentDefinitionList, len(newField.Arguments))

			for j, arg := range field.Arguments {
				updatedArg := *arg
				updatedArg.Directives = _removeReplacesDirective(updatedArg.Directives)
				newField.Arguments[j] = &updatedArg
			}
		}
		// Clear @replaces directives on enum values.
		//
		// For renamed enums, we emit the enum with the existing new enum
		// values and then later emit a enum extension that adds in any old
		// enum values, for example:
		//
		// enum OldEnumName { EnumValueOne  EnumValueTwo }
		// extend OldEnumName { OldEnumValueTwo }
		//
		// which results in the enum:
		//
		// enum OldEnumName { EnumValueOne, EnumValueTwo, OldEnumValueTwo }
		for i, enumValue := range definitionInfo.definition.EnumValues {
			newEnumValue := *enumValue
			newEnumValue.Directives = _removeReplacesDirective(newEnumValue.Directives)
			oldDefinition.EnumValues[i] = &newEnumValue
		}
		f.FormatDefinition(&oldDefinition, hasExtend)
		buf.WriteByte('\n')
	}

	// Field updates
	//
	// This is where we emit type extensions for old field names. If a type was
	// NOT renamed, we emit a single type extension on the existing type. If a
	// type was renamed, we emit type extensions for both the new and old type
	// names; the old type was emitted above in the definitions section without
	// the old field names.
	//
	// For example, this is an old field "coachKaid" on a type "Classroom" that
	// hasn't been renamed:
	//
	// type Classroom { id: ID! teacherKaid: String! }
	// extend type Classroom { coachKaid: String! @deprecated(...) }
	//
	// And this is an old field "coachKaid" on a type "Classroom" that has been
	// renamed from "StudentList":
	//
	// type Classroom   { id: ID! teacherKaid: String! }
	// type StudentList { id: ID! teacherKaid: String! }
	// extend type Classroom   { coachKaid: String! @deprecated(...) }
	// extend type StudentList { coachKaid: String! @deprecated(...) }
	fieldsObjectNames := make([]string, 0, len(r.fields))
	for objectName := range r.fields {
		fieldsObjectNames = append(fieldsObjectNames, objectName)
	}
	sort.Strings(fieldsObjectNames)

	for _, newObjectName := range fieldsObjectNames {
		fields := r.fields[newObjectName]

		// If the object the fields are on has also been renamed, output
		// renamed fields for both new and old object names.
		allObjectNames := []string{newObjectName}
		if oldName, ok := r.cacheReplacedTypes[newObjectName]; ok {
			allObjectNames = append(allObjectNames, oldName)
		}

		// We make a copy of the keys and update them in-place if a renamed
		// field is present in a key. Any updated keys are added to the type
		// extension.
		keys := make([]string, len(r.federationKeys[newObjectName]))
		copy(keys, r.federationKeys[newObjectName])
		keyHasUpdates := make([]bool, len(keys))

		for _, objectName := range allObjectNames {
			object := ast.Definition{
				Kind: r.definitionKinds[newObjectName],
				Name: objectName,
			}
			for _, fieldInfo := range fields {
				oldField := *fieldInfo.field
				oldField.Name = fieldInfo.oldName
				if fieldInfo.oldTypeName != "" {
					oldField.Type = _updateType(fieldInfo.field.Type, fieldInfo.oldTypeName)
				}

				for i := range keys {
					// Note: if a renamed field name appears in two places in
					// the federation key, e.g. `id { id }`, we'll replace both
					// instances of the name, which isn't correct (we only want
					// to replace the field belonging to the object). This case
					// is pretty rare, and we don't expect to encounter it in
					// practice.
					if _containsExactWord(keys[i], fieldInfo.field.Name) {
						keys[i] = _replaceExactWord(
							keys[i], fieldInfo.field.Name, fieldInfo.oldName)
						keyHasUpdates[i] = true
					}
				}

				// Apply any argument renames. Note: renamed arguments are only
				// allowed on renamed fields, i.e. if an argument is renamed,
				// the corresponding field must also be renamed. This
				// requirement is enforced above when processing fields.
				oldField.Arguments = make(
					ast.ArgumentDefinitionList, len(fieldInfo.field.Arguments))
				for i, argument := range fieldInfo.field.Arguments {
					oldArgument := *argument
					oldField.Arguments[i] = &oldArgument

					replaceInfo, ok := r.getReplaceInfo(oldArgument.Directives)
					if !ok {
						continue
					}

					oldArgument.Name = replaceInfo.OldName
					oldArgument.Directives = _removeReplacesDirective(oldArgument.Directives)

					if replaceInfo.OldTypeName != "" {
						oldArgument.Type = _updateType(argument.Type, replaceInfo.OldTypeName)
					}
				}
				oldField.Directives = _removeReplacesDirective(oldField.Directives)

				deprecatedMessage := fmt.Sprintf("Replaced by %s.", fieldInfo.field.Name)
				// The @deprecated directive isn't valid on input fields.
				if r.definitionKinds[newObjectName] != ast.InputObject {
					oldField.Directives = _addDeprecatedDirective(
						oldField.Directives, deprecatedMessage)
				} else {
					if oldField.Description == "" {
						oldField.Description = "Deprecated: " + deprecatedMessage
					} else {
						oldField.Description = oldField.Description +
							"\nDeprecated: " + deprecatedMessage
					}
				}
				oldField.Directives = append(oldField.Directives, &ast.Directive{
					Name: "goField",
					Arguments: ast.ArgumentList{
						&ast.Argument{
							Name: "name",
							Value: &ast.Value{
								Kind: ast.StringValue,
								Raw:  "Deprecated" + strings.Title(fieldInfo.oldName),
							},
						},
					},
				})
				object.Fields = append(object.Fields, &oldField)
			}

			// Add any updated keys to the type extension. Directives on type
			// extensions are additive; any updated keys will be present on
			// the type along with the original keys.
			for i := range keys {
				if keyHasUpdates[i] {
					object.Directives = append(object.Directives, &ast.Directive{
						Name: "key",
						Arguments: ast.ArgumentList{
							&ast.Argument{
								Name: "fields",
								Value: &ast.Value{
									Kind: ast.StringValue,
									Raw:  keys[i],
								},
							},
						},
					})
				}
			}

			f.FormatDefinition(&object, true)
			buf.WriteByte('\n')
		}
	}

	// Enum value updates
	//
	// We emit enum extensions that to add old enum values to both new
	// (and possibly old) enum types.
	enumValuesEnumNames := make([]string, 0, len(r.enumValues))
	for enumName := range r.enumValues {
		enumValuesEnumNames = append(enumValuesEnumNames, enumName)
	}
	sort.Strings(enumValuesEnumNames)

	for _, newName := range enumValuesEnumNames {
		enumValues := r.enumValues[newName]

		// If the enum the enum values are on has also been renamed, output
		// renamed enum values for both new and old enum names.
		allEnumNames := []string{newName}
		if oldName, ok := r.cacheReplacedTypes[newName]; ok {
			allEnumNames = append(allEnumNames, oldName)
		}

		for _, enumName := range allEnumNames {
			enum := ast.Definition{
				Kind: ast.Enum,
				Name: enumName,
			}
			for _, enumValueInfo := range enumValues {
				// Make a copy of the existing enum to retain any existing
				// directives.
				oldEnumValue := *enumValueInfo.enumValue
				oldEnumValue.Name = enumValueInfo.oldName
				oldEnumValue.Directives = _removeReplacesDirective(oldEnumValue.Directives)
				oldEnumValue.Directives = _addDeprecatedDirective(
					oldEnumValue.Directives,
					fmt.Sprintf("Replaced by %s.", enumValueInfo.newName))
				enum.EnumValues = append(enum.EnumValues, &oldEnumValue)
			}
			f.FormatDefinition(&enum, true)
			buf.WriteByte('\n')
		}
	}

	// Interface implementation updates
	extraImplementsObjectNames := make([]string, 0, len(r.extraImplements))
	for objectName := range r.extraImplements {
		extraImplementsObjectNames = append(extraImplementsObjectNames, objectName)
	}
	sort.Strings(extraImplementsObjectNames)

	for _, newName := range extraImplementsObjectNames {
		interfaceNames := r.extraImplements[newName]

		// If this object, which implements the renamed interface, has also
		// been renamed, output extra interfaces for both new and old object
		// names.
		allObjectNames := []string{newName}
		if oldName, ok := r.cacheReplacedTypes[newName]; ok {
			allObjectNames = append(allObjectNames, oldName)
		}

		for _, objectName := range allObjectNames {
			object := ast.Definition{
				Kind: ast.Object,
				Name: objectName,
			}
			object.Interfaces = append(object.Interfaces, interfaceNames...)
			f.FormatDefinition(&object, true)
			buf.WriteByte('\n')
		}
	}

	// Union member updates
	//
	// We emit union extensions that to add old union members to both new
	// (and possibly old) union types. For example:
	//
	// union SomeUnion = MemberOne | MemberTwo
	// extend SomeUnion = OldMemberTwo
	//
	// Results in the union:
	// union SomeUnion = MemberOne | MemberTwo | OldMemberTwo
	extraUnionMembersUnionNames := make([]string, 0, len(r.extraUnionMembers))
	for unionName := range r.extraUnionMembers {
		extraUnionMembersUnionNames = append(extraUnionMembersUnionNames, unionName)
	}
	sort.Strings(extraUnionMembersUnionNames)

	for _, newName := range extraUnionMembersUnionNames {
		unionMembers := r.extraUnionMembers[newName]

		// If the union the union members are on has also been renamed, output
		// the extra union members for both new and old union names.
		allUnionNames := []string{newName}
		if oldName, ok := r.cacheReplacedTypes[newName]; ok {
			allUnionNames = append(allUnionNames, oldName)
		}

		for _, unionName := range allUnionNames {
			union := ast.Definition{
				Kind: ast.Union,
				Name: unionName,
			}
			union.Types = append(union.Types, unionMembers...)
			f.FormatDefinition(&union, true)
			buf.WriteByte('\n')
		}
	}

	return strings.ReplaceAll(buf.String(), "\t", "    ")
}

// We expect "extend" and the definition keyword to be on the same line.
// GraphQL doesn't require this, but it prevents us from picking up "extend"
// at the end of a comment. Note that "extend" does NOT have to be the first
// word on the line containing the definition; a line can be preceded by some
// other definition. See the tests for examples.
var _extendRegex = regexp.MustCompile(`\bextend[ \t]+\w+\s+$`)

// _definitionHasExtends returns true in the given definition uses the "extend"
// keyword.
//
// Note that gqlparser doesn't track whether a definition uses the "extend"
// keyword, so we look at the source to see if the extend keyword is present
// in the source before the definition start.
func _definitionHasExtends(definition *ast.Definition) bool {
	// Grab a sub-string for the definition that's long enough to contain the
	// extend keyword and definition keyword . Add some extra characters to
	// account for any additional white space that may be present. "interface"
	// is the longest keyword used with a definition, so we use length long
	// enough to contain that word.
	prefixLength := len("extend") + len("interface") + 10
	// Position.Start is the index of the definition name. For example, in
	// `extend type StudentList {` Start points at the position before `S` in
	// `StudentList`.
	prefixStart := definition.Position.Start - prefixLength
	if prefixStart < 0 {
		prefixLength += prefixStart
		prefixStart = 0
	}
	substring := definition.Position.Src.Input[prefixStart : prefixStart+prefixLength]
	// Look for a prefix string that contains "extend" and ends with the
	// definition keyword followed by white space, e.g. "extend type ".

	return _extendRegex.FindString(substring) != ""
}

func _containsExactWord(text string, word string) bool {
	// The inputs are GraphQL field names, which won't have any characters that
	// need to be escaped.
	regex := regexp.MustCompile(`\b` + word + `\b`)
	return regex.FindString(text) != ""
}

func _replaceExactWord(text string, word string, replacement string) string {
	// The inputs are GraphQL field names, which won't have any characters that
	// need to be escaped.
	regex := regexp.MustCompile(`\b` + word + `\b`)
	return regex.ReplaceAllString(text, replacement)
}

// _updateType returns a new type with the same shape as the passed in type but
//
//	with the inner named type replaced with the new type name. "Same shape"
//	means that non-nulls and list nesting are preserved.
func _updateType(typ *ast.Type, newTypeName string) *ast.Type {
	if typ.NamedType != "" {
		return &ast.Type{NamedType: newTypeName, NonNull: typ.NonNull}
	}

	return &ast.Type{
		NonNull: typ.NonNull,
		Elem:    _updateType(typ.Elem, newTypeName),
	}
}

func _removeReplacesDirective(directives ast.DirectiveList) ast.DirectiveList {
	if directives == nil {
		return nil
	}
	updated := make(ast.DirectiveList, 0, len(directives)-1)
	for _, directive := range directives {
		if directive.Name != "replaces" {
			updated = append(updated, directive)
		}
	}
	return updated
}

func _addDeprecatedDirective(directives ast.DirectiveList, message string) ast.DirectiveList {
	updated := make(ast.DirectiveList, len(directives), len(directives)+1)
	copy(updated, directives)

	return append(updated, &ast.Directive{
		Name: "deprecated",
		Arguments: ast.ArgumentList{
			&ast.Argument{
				Name: "reason",
				Value: &ast.Value{
					Kind: ast.StringValue,
					Raw:  message,
				},
			},
		},
	})
}
