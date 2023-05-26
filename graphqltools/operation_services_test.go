package graphqltools

import (
	"github.com/vektah/gqlparser/v2"
	"os"
	"path"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Khan/webapp/dev/khantest"
)

type operationServicesSuite struct {
	khantest.Suite
	schema *ast.Schema
}

func (suite *operationServicesSuite) SetupSuite() {
	suite.Suite.SetupSuite()

	schemaPath := path.Join(khantest.TestdataDir(), "schema.graphql")
	schemaContent, err := os.ReadFile(schemaPath)
	suite.Require().NoError(err)

	source := &ast.Source{
		Name:  "schema.graphql",
		Input: string(schemaContent),
	}

	// Note: gqlparserErr has a concrete error type, which is why we assign it
	// to a non-interface variable.
	schema, err := gqlparser.LoadSchema(source)
	suite.Require().NoError(err)

	suite.schema = schema
}

func (suite *operationServicesSuite) TestNonFederatedTypeSingleService() {
	const query = `
		query {
			serviceAThing {
				name
				color {
					name
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal([]string{"serviceA"}, services)
}

func (suite *operationServicesSuite) TestFederatedTypeSingleService() {
	const query = `
		query {
			serviceAFederatedThing {
				serviceAField {
					name
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().Equal([]string{"serviceA"}, services)
}

func (suite *operationServicesSuite) TestFederatedTypeSingleServiceProvidesField() {
	const query = `
		query {
			serviceAFederatedThing {
				serviceBFederatedThing {
					# Note: serviceA provides this field. That means it isn't
					# necessary to communicate with serviceB to resolve this
					# query, though we don't currently support this case.
					serviceBField
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	// If we add support for @provides, change this to just "serviceA".
	suite.Require().Equal([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestFederatedTypeSingleServiceKeyField() {
	const query = `
		query {
			serviceAFederatedThing {
				serviceBFederatedThing {
					# We only select the key, which is implicitly provided by
					# serviceA. That means it isn't necessary to communicate
					# with serviceB to resolve this query, though we don't
					# currently support this case.
					id
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	// If we add support for key-only selections, change this to "serviceA".
	suite.Require().Equal([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestFederatedTypeMultipleServices() {
	const query = `
		query {
			serviceAFederatedThing {
				serviceBField {
					name
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestInterfaceSingleService() {
	const query = `
		query {
			sameServiceOwnerInterface {
				serviceAField
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA"}, services)
}

func (suite *operationServicesSuite) TestInterfaceMultipleServices() {
	const query = `
		query {
			sameServiceOwnerInterface {
				serviceBField
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestInterfaceMultipleServicesFragment() {
	const query = `
		query {
			sameServiceOwnerInterface {
				serviceAField
				...Fragment
				
			}
		}
		fragment Fragment on SameServiceOwnerConcreteTwo {
			fieldOnlyInServiceB
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestInterfaceMultipleServicesInlineFragment() {
	const query = `
		query {
			sameServiceOwnerInterface {
				serviceAField
				... on SameServiceOwnerConcreteTwo {
					fieldOnlyInServiceB
				}
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestInterfaceMultipleServicesMixedOwner() {
	const query = `
		query {
			mixedServiceOwnerInterface {
				mixedOwnershipField
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func (suite *operationServicesSuite) TestInterfaceResolvedByNonOwner() {
	const query = `
		query {
			# This field is resolved by serviceB but the interface is
			# effectively owned by serviceA (i.e. all the concrete types are
			# owned by serviceA).
			interfaceResolvedByNonOwner {
				serviceAField
			}
		}
	`

	services, err := ServicesForOperation(suite.schema, query)
	suite.Require().NoError(err)

	suite.Require().ElementsMatch([]string{"serviceA", "serviceB"}, services)
}

func TestOperationServices(t *testing.T) {
	khantest.Run(t, new(operationServicesSuite))
}
