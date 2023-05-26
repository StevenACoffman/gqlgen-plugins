// Package graphqltools contains tools for analyzing GraphQL operations.
package graphqltools

import (
	"fmt"
	"github.com/StevenACoffman/gqlgen-plugins/errors/kind"
	"sort"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/StevenACoffman/simplerr/errors"
)

// ServicesForOperation returns the services used to resolve the query in the
// given query text according to the provided composed schema, i.e. a schema in
// the CSDL format.
//
// Note: the CSDL format is deprecated, but adapting this code to the new
// "join" format should be straight forward: https://specs.apollo.dev/join.
func ServicesForOperation(schema *ast.Schema, queryText string) ([]string, error) {
	query, errList := gqlparser.LoadQuery(schema, queryText)
	if errList != nil {
		return nil, errList
	}
	if len(query.Operations) != 1 {
		return nil, errors.Wrap(kind.Internal,
			"each query must contain exactly one operation")
	}
	operation := query.Operations[0]
	services := processSelectionSet(schema, operation.SelectionSet)
	servicesList := make([]string, 0, len(services))
	for service := range services {
		servicesList = append(servicesList, service)
	}
	// Sort the list of services so the return order is deterministic for
	// tests.
	sort.Strings(servicesList)
	return servicesList, nil
}

type uniqueServices map[string]bool

// processSelectionSet returns service ownership for the fields in the given
// selection set (including fields in fragments and inline fragments
// recursively).
func processSelectionSet(schema *ast.Schema, selectionSet ast.SelectionSet) uniqueServices {
	services := make(uniqueServices)
	for _, selection := range selectionSet {
		switch v := selection.(type) {
		case *ast.Field:
			// We include both the owner(s) of the object the field belongs to
			// and the owner of the field because when a type is federated the
			// federation keys and @requires fields are selected by the gateway
			// and these fields are always owned by the object owner.
			//
			// Note that this logic doesn't take into account @provides or
			// @key directives. A query that exclusively selects @provides
			// and @key fields doesn't need to communicate with the owning
			// service. We ignore this case, which is okay for our purposes,
			// because ignoring it is a conservative assumption (i.e. service
			// mappings may include services that aren't strictly necessary,
			// but they'll always include services that are necessary).
			objectServices := servicesForType(schema, v.ObjectDefinition)
			for _, service := range objectServices {
				services[service] = true
			}
			fieldService := serviceForField(schema, v.ObjectDefinition, v.Definition)
			if fieldService != "" {
				services[fieldService] = true
			}
			for service := range processSelectionSet(schema, v.SelectionSet) {
				services[service] = true
			}
		case *ast.FragmentSpread:
			for service := range processSelectionSet(schema, v.Definition.SelectionSet) {
				services[service] = true
			}
		case *ast.InlineFragment:
			for service := range processSelectionSet(schema, v.SelectionSet) {
				services[service] = true
			}
		}
	}
	return services
}

// serviceForField returns the service indicated by the @join__field
// directive on the given field, if any. Note: if there is no join__field
// directive, the field is owned by the object that contains the field.
func serviceForField(
	schema *ast.Schema,
	objectDefinition *ast.Definition,
	fieldDefinition *ast.FieldDefinition,
) string {
	if objectDefinition.Kind == ast.Interface {
		return serviceForInterfaceField(schema, objectDefinition, fieldDefinition.Name)
	}
	for _, directive := range fieldDefinition.Directives {
		if directive.Name == "join__field" {
			for _, argument := range directive.Arguments {
				if argument.Name == "graph" {
					return serviceNameFromEnum(schema, argument.Value.Raw)
				}
			}
		}
	}
	return ""
}

// serviceForInterfaceField returns the service that "owns" the named field on
// the given interface. Ownership is determined by looking at the matching
// fields on the concrete types. This function enforces that all fields on the
// concrete types with the same name have the same owner.
func serviceForInterfaceField(
	schema *ast.Schema,
	objectDefinition *ast.Definition,
	fieldName string,
) string {
	var service string
	var previousConcreteTypeName string
	for _, concreteType := range schema.PossibleTypes[objectDefinition.Name] {
		for _, field := range concreteType.Fields {
			if field.Name != fieldName {
				continue
			}
			isFirstConcreteType := previousConcreteTypeName == ""
			serviceForThisType := serviceForField(schema, concreteType, field)
			if !isFirstConcreteType && serviceForThisType != service {
				panic(fmt.Sprintf(
					"%s interface field \"%s\" has concrete "+
						"implementations owned by different services. "+
						"The field is owned by the \"%s\" service on %s "+
						"but by the \"%s\" service on %s.",
					objectDefinition.Name,
					fieldName,
					service,
					previousConcreteTypeName,
					serviceForThisType,
					concreteType.Name,
				))
			}
			service = serviceForThisType
			previousConcreteTypeName = concreteType.Name
			break
		}
	}
	return service
}

// Return the service for the given type. The type may be an object, or
// abstract type (i.e. an interface or union). In the case of abstract types,
// the service owners for each of the concrete types is returned.
func servicesForType(schema *ast.Schema, objectDefinition *ast.Definition) []string {
	var services []string
	// PossibleTypes is all the possible types for an abstract type. An
	// abstract type is an interface or union. For non-abstract types,
	// PossibleTypes contains the concrete type itself.
	for _, concreteType := range schema.PossibleTypes[objectDefinition.Name] {
		service := serviceForConcreteType(schema, concreteType)
		if service != "" {
			services = append(services, service)
		}
	}
	return services
}

// serviceForConcreteType returns the value of the "join__owner"
// directive on the given type, if one exists. If there is no owner,
// either the type is owned by a single service or the type is a
// "value" type. For single-owner types, *some* parent selection
// should contain an owner. In both the single-owner and "value" type
// cases no additional service information is available, so this
// function returns an empty string.
func serviceForConcreteType(schema *ast.Schema, objectDefinition *ast.Definition) string {
	for _, directive := range objectDefinition.Directives {
		if directive.Name == "join__owner" {
			for _, argument := range directive.Arguments {
				if argument.Name == "graph" {
					return serviceNameFromEnum(schema, argument.Value.Raw)
				}
			}
		}
	}
	return ""
}

// serviceNameFromEnum maps the service-enum to its name.  The schema
// has directives like `@join__owner(graph: TEST_PREP)` and we want to
// map `TEST_PREP` to `"test-prep"`, the name of the service.  This
// function does this via the join__Graph enum.
func serviceNameFromEnum(schema *ast.Schema, enumName string) string {
	for _, enum := range schema.Types["join__Graph"].EnumValues {
		if enum.Name == enumName {
			for _, directive := range enum.Directives {
				if directive.Name == "join__graph" {
					for _, argument := range directive.Arguments {
						if argument.Name == "name" {
							return argument.Value.Raw
						}
					}
				}
			}
		}
	}
	panic(fmt.Sprintf("No join__Graph enum named '%s' found", enumName))
}
