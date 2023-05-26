package gqlgen_plugins

import (
	"context"
	"os"
	"testing"

	"github.com/99designs/gqlgen/codegen"
	"github.com/99designs/gqlgen/codegen/config"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Khan/webapp/dev/khantest"
	"github.com/Khan/webapp/pkg/lib"
)

type replacesSuite struct{ khantest.Suite }

var replacesDirecticeSource string

func parse(input string) (*ast.Schema, error) {
	if replacesDirecticeSource == "" {
		path := lib.KARootJoin(
			context.Background(), "pkg", "graphql", "shared-schemas", "replaces_directive.graphql")
		sourceBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		replacesDirecticeSource = string(sourceBytes)
	}
	input = replacesDirecticeSource + input
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: input})
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func (suite *replacesSuite) TestGetSchemaInfo() {
	schema, err := parse(`
		type NewDomain @replaces(name: "OldDomain") {
			id: ID!
		}

		input DomainInput {
			kaLocale: String @replaces(name: "locale", wasRequiredBeforeRename: true, treatZeroAsUnset: true)
			childCurationNodeIds: [String!] @replaces(name: "childTopics")
		}
	`)
	suite.Require().NoError(err)

	schemaInfo, err := _getSchemaInfo(schema)
	suite.Require().NoError(err)

	expected := &_schemaInfo{
		renamedTypes: map[string]*_typeInfo{
			"NewDomain": {
				kind:    ast.Object,
				newName: "NewDomain",
				oldName: "OldDomain",
			},
		},
		renamedFields: map[string]*_fieldInfoGroup{
			"DomainInput": {
				objectKind: ast.InputObject,
				fields: []*_fieldInfo{
					{
						newName:                 "kaLocale",
						oldName:                 "locale",
						wasRequiredBeforeRename: true,
						treatZeroAsUnset:        true,
					},
					{
						newName:                 "childCurationNodeIds",
						oldName:                 "childTopics",
						wasRequiredBeforeRename: false,
					},
				},
			},
		},
	}

	suite.Require().Equal(expected, schemaInfo)
}

func (suite *replacesSuite) TestValiateConfigObjectResolversMatch() {
	schemaInfo := &_schemaInfo{
		renamedTypes: map[string]*_typeInfo{
			"NewDomain": {
				kind:    ast.Object,
				newName: "NewDomain",
				oldName: "OldDomain",
			},
		},
	}

	cfg := &config.Config{
		Models: config.TypeMap{
			"NewDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"sourceKaLocale": {
						Resolver: true,
					},
				},
			},
			"OldDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"sourceKaLocale": {
						Resolver: true,
					},
				},
			},
		},
	}

	err := _validateConfig(cfg, schemaInfo)
	suite.Require().NoError(err)
}

func (suite *replacesSuite) TestValiateConfigObjectResolversDoNotMatch() {
	schemaInfo := &_schemaInfo{
		renamedTypes: map[string]*_typeInfo{
			"NewDomain": {
				kind:    ast.Object,
				newName: "NewDomain",
				oldName: "OldDomain",
			},
		},
	}

	cfg := &config.Config{
		Models: config.TypeMap{
			"NewDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"sourceKaLocale": {
						Resolver: true,
					},
				},
			},
			"OldDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"sourceKaLocale": {
						Resolver: false,
					},
				},
			},
		},
	}

	err := _validateConfig(cfg, schemaInfo)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(),
		"model configs don't match for renamed object, newName = NewDomain, oldName = OldDomain")
}

func (suite *replacesSuite) TestValiateConfigFieldOkay() {
	schemaInfo := &_schemaInfo{
		renamedFields: map[string]*_fieldInfoGroup{
			"DomainInput": {
				objectKind: ast.Object,
				fields: []*_fieldInfo{
					{
						newName: "kaLocale",
						oldName: "locale",
					},
				},
			},
		},
	}

	cfg := &config.Config{
		Models: config.TypeMap{
			"DomainInput": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"kaLocale": {
						Resolver: true,
					},
					"locale": {
						Resolver: true,
					},
				},
			},
		},
	}

	err := _validateConfig(cfg, schemaInfo)
	suite.Require().NoError(err)
}

func (suite *replacesSuite) TestValiateConfigFieldNewResolverMissing() {
	schemaInfo := &_schemaInfo{
		renamedFields: map[string]*_fieldInfoGroup{
			"NewDomain": {
				objectKind: ast.Object,
				fields: []*_fieldInfo{
					{
						newName: "kaLocale",
						oldName: "locale",
					},
				},
			},
		},
	}

	cfg := &config.Config{
		Models: config.TypeMap{
			"NewDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"locale": {
						Resolver: true,
					},
				},
			},
		},
	}

	err := _validateConfig(cfg, schemaInfo)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "renamed fields must have matching resolver configurations")
}

func (suite *replacesSuite) TestValiateConfigFieldOldResolverMissing() {
	schemaInfo := &_schemaInfo{
		renamedFields: map[string]*_fieldInfoGroup{
			"NewDomain": {
				objectKind: ast.Object,
				fields: []*_fieldInfo{
					{
						newName: "kaLocale",
						oldName: "locale",
					},
				},
			},
		},
	}

	cfg := &config.Config{
		Models: config.TypeMap{
			"NewDomain": config.TypeMapEntry{
				Fields: map[string]config.TypeMapField{
					"kaLocale": {
						Resolver: true,
					},
				},
			},
		},
	}

	err := _validateConfig(cfg, schemaInfo)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(), "renamed fields must have matching resolver configurations")
}

func (suite *replacesSuite) TestConstructTemplateDataConstructsObjectMapperData() {
	schemaInfo := &_schemaInfo{
		renamedTypes: map[string]*_typeInfo{
			"NewDomain": {
				kind:    ast.Object,
				newName: "NewDomain",
				oldName: "OldDomain",
			},
		},
	}

	data := &codegen.Data{
		Config: &config.Config{
			Models: config.TypeMap{
				"NewDomain": config.TypeMapEntry{
					Fields: map[string]config.TypeMapField{
						"subjectMastery": {
							FieldName: "CourseMastery",
						},
					},
				},
				"OldDomain": config.TypeMapEntry{
					Fields: map[string]config.TypeMapField{
						"subjectMastery": {
							FieldName: "CourseMastery",
						},
					},
				},
			},
		},
		Objects: codegen.Objects{
			{
				Definition: &ast.Definition{
					Name: "NewDomain",
				},
				Fields: []*codegen.Field{
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "subjectMastery",
						},
						GoFieldName: "SubjectMastery",
					},
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "id",
						},
						GoFieldName: "ID",
					},
				},
			},
			{
				Definition: &ast.Definition{
					Name: "OldDomain",
				},
				Fields: []*codegen.Field{
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "subjectMastery",
						},
						GoFieldName: "SubjectMastery",
					},
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "id",
						},
						GoFieldName: "ID",
					},
				},
			},
		},
	}

	templateData, err := _constructTemplateData(data, schemaInfo)
	suite.Require().NoError(err)

	expected := &_templateData{
		Objects: []_templateDataObjectMapper{
			{
				NewGoName: "NewDomain",
				OldGoName: "OldDomain",
				Fields: []string{
					"CourseMastery",
					"ID",
				},
			},
		},
	}

	suite.Require().Equal(expected, templateData)
}

func (suite *replacesSuite) TestConstructTemplateDataObjectFieldsDoNotMatch() {
	schemaInfo := &_schemaInfo{
		renamedTypes: map[string]*_typeInfo{
			"NewDomain": {
				kind:    ast.Object,
				newName: "NewDomain",
				oldName: "OldDomain",
			},
		},
	}

	data := &codegen.Data{
		Config: &config.Config{
			Models: config.TypeMap{
				"NewDomain": config.TypeMapEntry{
					Fields: map[string]config.TypeMapField{
						"subjectMastery": {
							FieldName: "CourseMastery",
						},
					},
				},
				"OldDomain": config.TypeMapEntry{
					Fields: map[string]config.TypeMapField{
						"subjectMastery": {
							FieldName: "CourseMastery",
						},
					},
				},
			},
		},
		Objects: codegen.Objects{
			{
				Definition: &ast.Definition{
					Name: "NewDomain",
				},
				Fields: []*codegen.Field{
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "subjectMastery",
						},
						GoFieldName: "SubjectMastery",
					},
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "id",
						},
						GoFieldName: "ID",
					},
				},
			},
			{
				Definition: &ast.Definition{
					Name: "OldDomain",
				},
				Fields: []*codegen.Field{
					{
						FieldDefinition: &ast.FieldDefinition{
							Name: "id",
						},
						GoFieldName: "ID",
					},
				},
			},
		},
	}

	_, err := _constructTemplateData(data, schemaInfo)
	suite.Require().Error(err)
	suite.Require().Contains(
		err.Error(),
		"[invalid input error] could not generate mapper for renamed type; fields do not match, newType = NewDomain, oldType = OldDomain",
	)
}

func TestReplacesDirective(t *testing.T) {
	khantest.Run(t, new(replacesSuite))
}
