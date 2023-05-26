package graphqltools

import (
	"github.com/vektah/gqlparser/v2"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Khan/webapp/dev/khantest"
)

const metadataSchema = `
schema {
  query: Query
}

directive @migrate(from: String!, state: String!) on FIELD_DEFINITION

type Query {
  testType: TestType!
}

type TestType {
  id: ID!
  scalarField: String!
  objectField: TestType!
  manualField: String! @migrate(from: "python", state: "manual")
  sideBySideField: String! @migrate(from: "python", state: "side-by-side")
  canaryField: String! @migrate(from: "python", state: "canary")
  migratedField: String! @migrate(from: "python", state: "migrated")
}
`

type operationMetadataSuite struct {
	khantest.Suite
	schema *ast.Schema
}

func (suite *operationMetadataSuite) SetupSuite() {
	suite.Suite.SetupSuite()

	source := &ast.Source{
		Name:  "<inline>",
		Input: string(metadataSchema),
	}

	// Note: gqlparserErr has a concrete error type, which is why we assign it
	// to a non-interface variable.
	schema, err := gqlparser.LoadSchema(source)
	suite.Require().NoError(err)

	suite.schema = schema
}

func (suite *operationMetadataSuite) TestNoMetadata() {
	const query = `
		query {
			testType {
				scalarField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataNested() {
	const query = `
		query {
			testType {
				objectField {
					scalarField
				}
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataManual() {
	const query = `
		query {
			testType {
				manualField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataMigrated() {
	const query = `
		query {
			testType {
				migratedField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestHasSideBySide() {
	const query = `
		query {
			testType {
				sideBySideField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasSideBySideFields: true}, metadata)
}

func (suite *operationMetadataSuite) TestHasSideBySideNested() {
	const query = `
		query {
			testType {
				objectField {
					sideBySideField
				}
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasSideBySideFields: true}, metadata)
}

func (suite *operationMetadataSuite) TestHasCanary() {
	const query = `
		query {
			testType {
				canaryField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasCanaryFields: true}, metadata)
}

func (suite *operationMetadataSuite) TestHasCanaryNested() {
	const query = `
		query {
			testType {
				objectField {
					canaryField
				}
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasCanaryFields: true}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataAlias() {
	const query = `
		query {
			testType {
				rename: scalarField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataMultipleAliases() {
	const query = `
		query {
			testType {
				rename: scalarField
				rename2: scalarField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func (suite *operationMetadataSuite) TestHasMixedAliases() {
	const query = `
		query {
			testType {
				scalarField
				rename: scalarField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasMixedAliases: true}, metadata)
}

func (suite *operationMetadataSuite) TestHasMixedAliasesInFragment() {
	const query = `
		query {
			testType {
				scalarField
				... on TestType {
					rename: scalarField
				}
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{HasMixedAliases: true}, metadata)
}

func (suite *operationMetadataSuite) TestNoMetadataMixedAliasesAtDifferentLevels() {
	const query = `
		query {
			testType {
				objectField {
					scalarField
				}
				rename: scalarField
			}
		}
	`

	metadata, err := MetadataForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal(OperationMetadata{}, metadata)
}

func TestOperationMetadata(t *testing.T) {
	khantest.Run(t, new(operationMetadataSuite))
}
