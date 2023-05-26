package gqlgen_plugins

// This file contains the ReplacesDirective plugin, below.
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

import (
	_ "embed"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/99designs/gqlgen/codegen"
	"github.com/99designs/gqlgen/codegen/config"
	"github.com/99designs/gqlgen/codegen/templates"
	"github.com/99designs/gqlgen/plugin"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/StevenACoffman/gqlgen-plugins/errors/kind"
	"github.com/StevenACoffman/gqlgen-plugins/graphqltools"
	"github.com/StevenACoffman/simplerr/errors"
)

// ReplacesDirective is a plugin that performs validation and code generation
// related to the @replaces directive. The @replaces directive is used to help
// rename fields in our GraphQL schema.
//
// The plugin does following:
//   - gqlgen resolver validation checks, and
//   - code generation of input "validate and rename" functions
//
// The plugin does NOT:
//   - keep services/deprecated.graphql files up to date
//     (for that, run `go run dev/cmd/get-replaces-directive-updates/main.go`)
//   - update resolver code to resolve rename fields
//
// See the directive in pkg/graphql/shared-schemas/replaces_directive.graphql
// for more information.
type ReplacesDirective struct {
	schemaInfo *_schemaInfo
}

type _schemaInfo struct {
	renamedTypes  map[string]*_typeInfo
	renamedFields map[string]*_fieldInfoGroup
}

func (s *_schemaInfo) hasInputObjectFieldRenames() bool {
	for _, fieldGroup := range s.renamedFields {
		if fieldGroup.objectKind == ast.InputObject {
			return true
		}
	}
	return false
}

func (s *_schemaInfo) hasObjectRenames() bool {
	for _, typeInfo := range s.renamedTypes {
		if typeInfo.kind == ast.Object {
			return true
		}
	}
	return false
}

type _typeInfo struct {
	kind    ast.DefinitionKind
	oldName string
	newName string
}

type _fieldInfoGroup struct {
	objectKind ast.DefinitionKind
	fields     []*_fieldInfo
}

type _fieldInfo struct {
	newName                 string
	oldName                 string
	wasRequiredBeforeRename bool
	treatZeroAsUnset        bool
}

var (
	_ plugin.Plugin        = (*ReplacesDirective)(nil)
	_ plugin.ConfigMutator = (*ReplacesDirective)(nil)
	_ plugin.CodeGenerator = (*ReplacesDirective)(nil)
)

func (r *ReplacesDirective) Name() string { return "replaces_directive" }

// Note: this plugin doesn't mutate the config; instead it uses this hook to
// validate that the config meets certain conditions. Specifically, we require
// new fields that replace old fields in the config to have the
// same "resolver" configuration. If an old field uses a resolver, the new
// renamed field must as well.
func (r *ReplacesDirective) MutateConfig(cfg *config.Config) error {
	schemaInfo, err := _getSchemaInfo(cfg.Schema)
	if err != nil {
		return err
	}

	// Cache schema info so it can be used by GenerateCode, which is called
	// later.
	r.schemaInfo = schemaInfo

	return _validateConfig(cfg, schemaInfo)
}

func _validateConfig(cfg *config.Config, schemaInfo *_schemaInfo) error {
	// First, check that renamed fields have the same resolver configuration as
	// the corresponding old field name. That is, if the config has an entry
	// like:
	//
	// models:
	//   User:
	//     fields:
	//       locale:
	//         resolver: true
	//
	// and "locale" is the old (or new) name for "kaLocale", then the following
	// configuration must also be present:
	//
	//   User:
	//     fields:
	//       kaLocale:
	//         resolver: true
	for newObjectName, fieldGroup := range schemaInfo.renamedFields {
		if fieldGroup.objectKind != ast.Object {
			continue
		}

		allObjectNames := []string{newObjectName}
		if typeInfo, ok := schemaInfo.renamedTypes[newObjectName]; ok {
			allObjectNames = append(allObjectNames, typeInfo.oldName)
		}

		for _, objectName := range allObjectNames {
			for _, fieldInfo := range fieldGroup.fields {
				newFieldHasResolver := _hasResolver(cfg, objectName, fieldInfo.newName)
				oldFieldHasResolver := _hasResolver(cfg, objectName, fieldInfo.oldName)
				if newFieldHasResolver != oldFieldHasResolver {
					return errors.WrapWithFields(kind.Internal,
						errors.Fields{
							"message":             "renamed fields must have matching resolver configurations",
							"objectName":          objectName,
							"newFieldName":        fieldInfo.newName,
							"newFieldHasResolver": newFieldHasResolver,
							"oldFieldName":        fieldInfo.oldName,
							"oldFieldHasResolver": oldFieldHasResolver,
						},
					)
				}
			}
		}
	}

	// Next, check that model configs match for old and new object names
	for _, typeInfo := range schemaInfo.renamedTypes {
		if typeInfo.kind != ast.Object {
			continue
		}
		if !reflect.DeepEqual(
			cfg.Models[typeInfo.newName].Fields, cfg.Models[typeInfo.oldName].Fields) {
			return errors.WrapWithFields(kind.InvalidInput,
				errors.Fields{
					"message": "model configs don't match for renamed object",
					"newName": typeInfo.newName,
					"oldName": typeInfo.oldName,
				},
			)
		}
	}
	return nil
}

func _hasResolver(cfg *config.Config, objectName string, fieldName string) bool {
	objectConfig, ok := cfg.Models[objectName]
	if !ok {
		return false
	}
	fieldConfig, ok := objectConfig.Fields[fieldName]
	if !ok {
		return false
	}
	return fieldConfig.Resolver
}

func _getSchemaInfo(schema *ast.Schema) (*_schemaInfo, error) {
	err := graphqltools.ValidateReplacesDirectives(schema)
	if err != nil {
		return nil, err
	}
	replacements := &_schemaInfo{
		renamedTypes:  make(map[string]*_typeInfo),
		renamedFields: make(map[string]*_fieldInfoGroup),
	}
	for _, definition := range schema.Types {
		switch definition.Kind {
		case ast.Object, ast.InputObject:
			replaceInfo, err := graphqltools.GetReplaceInfo(definition.Directives)
			if err != nil && !errors.Is(err, kind.NotFound) {
				return nil, err
			}
			if err == nil {
				replacements.renamedTypes[definition.Name] = &_typeInfo{
					kind:    definition.Kind,
					newName: definition.Name,
					oldName: replaceInfo.OldName,
				}
			}
			for _, field := range definition.Fields {
				replaceInfo, err := graphqltools.GetReplaceInfo(field.Directives)
				if errors.Is(err, kind.NotFound) {
					continue
				} else if err != nil {
					return nil, err
				}
				if _, ok := replacements.renamedFields[definition.Name]; !ok {
					replacements.renamedFields[definition.Name] = &_fieldInfoGroup{
						objectKind: definition.Kind,
					}
				}
				replacements.renamedFields[definition.Name].fields = append(
					replacements.renamedFields[definition.Name].fields,
					&_fieldInfo{
						newName:                 field.Name,
						oldName:                 replaceInfo.OldName,
						wasRequiredBeforeRename: replaceInfo.WasRequiredBeforeRename,
						treatZeroAsUnset:        replaceInfo.TreatZeroAsUnset,
					},
				)
			}
		}
	}
	return replacements, nil
}

//go:embed replaces_directive.gotpl
var _template string

type _templateData struct {
	Objects      []_templateDataObjectMapper
	InputObjects []_templateDataInputObject
}

type _templateDataInputObject struct {
	Name   string
	Fields []_templateDataField
}

type _templateDataObjectMapper struct {
	NewGoName string
	OldGoName string
	Fields    []string
}

type _templateDataField struct {
	NewName                 string
	OldName                 string
	NewGoName               string
	OldGoName               string
	WasRequiredBeforeRename bool
	TreatZeroAsUnset        bool
}

func (r *ReplacesDirective) GenerateCode(data *codegen.Data) error {
	genfilePath := filepath.Join(filepath.Dir(data.Config.Exec.Filename), "replaces_directive.go")

	// If there are no replacements, remove any existing generated file, and
	// we're done.
	if !r.schemaInfo.hasInputObjectFieldRenames() && !r.schemaInfo.hasObjectRenames() {
		err := os.Remove(genfilePath)
		// There's nothing to remove if the file has never been generated!
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WithStack(err)
	}

	templateData, err := _constructTemplateData(data, r.schemaInfo)
	if err != nil {
		return err
	}

	err = templates.Render(templates.Options{
		PackageName:     data.Config.Exec.Package,
		Filename:        genfilePath,
		GeneratedHeader: true, // include "DO NOT EDIT" line
		Template:        _template,
		Data:            templateData,
		Packages:        data.Config.Packages,
	})
	return errors.WithStack(err)
}

func _constructTemplateData(data *codegen.Data, schemaInfo *_schemaInfo) (*_templateData, error) {
	var templateData _templateData

	// Construct object mappers
	var objectMapperData []_templateDataObjectMapper
	for _, typeInfo := range schemaInfo.renamedTypes {
		if typeInfo.kind != ast.Object {
			continue
		}

		newObject := data.Objects.ByName(typeInfo.newName)
		if newObject == nil {
			return nil, errors.WrapWithFields(kind.Internal,
				errors.Fields{
					"message": "missing object in schema",
					"type":    typeInfo.newName})
		}
		oldObject := data.Objects.ByName(typeInfo.oldName)
		if oldObject == nil {
			return nil, errors.WrapWithFields(kind.Internal,
				errors.Fields{"message": "missing object in schema",
					"type": typeInfo.oldName})
		}

		newFields := make([]string, len(newObject.Fields))
		oldFields := make([]string, len(oldObject.Fields))

		for i, field := range newObject.Fields {
			name := field.GoFieldName
			nameOveride := data.Config.Models[newObject.Name].Fields[field.Name].FieldName
			if nameOveride != "" {
				name = nameOveride
			}
			newFields[i] = name
		}
		for i, field := range oldObject.Fields {
			name := field.GoFieldName
			nameOveride := data.Config.Models[oldObject.Name].Fields[field.Name].FieldName
			if nameOveride != "" {
				name = nameOveride
			}
			oldFields[i] = name
		}

		sort.Slice(newFields, func(i, j int) bool { return newFields[i] < newFields[j] })
		sort.Slice(oldFields, func(i, j int) bool { return oldFields[i] < oldFields[j] })

		if !reflect.DeepEqual(newFields, oldFields) {
			return nil, errors.WrapWithFields(kind.InvalidInput,
				errors.Fields{"message": "could not generate mapper for renamed type; fields do not match", "newType": typeInfo.newName, "oldType": typeInfo.oldName},
			)
		}

		objectMapperData = append(objectMapperData, _templateDataObjectMapper{
			NewGoName: newObject.Name, // Assume the GraphQL and Go name match
			OldGoName: oldObject.Name, // Assume the GraphQL and Go name match
			Fields:    newFields,      // Old and new fields are the same!
		})
	}
	templateData.Objects = objectMapperData

	// Construct input object mappers
	for newObjectName, fieldGroup := range schemaInfo.renamedFields {
		if fieldGroup.objectKind != ast.InputObject {
			continue
		}

		inputObject := _templateDataInputObject{
			Name: newObjectName,
		}

		for _, fieldInfo := range fieldGroup.fields {
			newFieldData, err := _getInputField(data, newObjectName, fieldInfo.newName)
			if err != nil {
				return nil, err
			}
			oldFieldData, err := _getInputField(data, newObjectName, fieldInfo.oldName)
			if err != nil {
				return nil, err
			}

			newType := newFieldData.TypeReference.GO.String()
			oldType := oldFieldData.TypeReference.GO.String()

			if newType != oldType {
				return nil, errors.WrapWithFields(kind.NotImplemented,
					errors.Fields{
						"message":  "don't know how to map between different input type fields",
						"newField": fieldInfo.newName,
						"oldField": fieldInfo.oldName,
					},
				)
			}

			inputObject.Fields = append(inputObject.Fields, _templateDataField{
				NewName:                 fieldInfo.newName,
				OldName:                 fieldInfo.oldName,
				NewGoName:               newFieldData.GoFieldName,
				OldGoName:               oldFieldData.GoFieldName,
				WasRequiredBeforeRename: fieldInfo.wasRequiredBeforeRename,
				TreatZeroAsUnset:        fieldInfo.treatZeroAsUnset,
			})
		}

		// Make sure field order in the generated file is stable.
		sort.Slice(inputObject.Fields, func(i, j int) bool {
			return inputObject.Fields[i].NewName < inputObject.Fields[j].NewName
		})

		templateData.InputObjects = append(templateData.InputObjects, inputObject)

		// Also generate code for the old object name, if there is one.
		if typeInfo, ok := schemaInfo.renamedTypes[newObjectName]; ok {
			oldInputObject := inputObject
			oldInputObject.Name = typeInfo.oldName

			templateData.InputObjects = append(templateData.InputObjects, oldInputObject)
		}
	}

	// Make sure object order in the generated file is stable.
	sort.Slice(templateData.Objects, func(i, j int) bool {
		return templateData.Objects[i].NewGoName < templateData.Objects[j].NewGoName
	})
	sort.Slice(templateData.InputObjects, func(i, j int) bool {
		return templateData.InputObjects[i].Name < templateData.InputObjects[j].Name
	})

	return &templateData, nil
}

func _getInputField(
	data *codegen.Data,
	objectName string,
	fieldName string,
) (*codegen.Field, error) {
	for _, object := range data.Inputs {
		if object.Definition.Name == objectName {
			for _, field := range object.Fields {
				if field.FieldDefinition.Name == fieldName {
					return field, nil
				}
			}
		}
	}
	return nil, errors.WrapWithFields(kind.NotFound, errors.Fields{
		"message":    "input object field not found",
		"objectName": objectName,
		"fieldName":  fieldName,
	})
}
