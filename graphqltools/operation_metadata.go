package graphqltools

// This file contains tools for extracting "operation metadata" for a GraphQL
// operation. See the OperationMetadata type for metadata that's available.

import (
	"github.com/StevenACoffman/gqlgen-plugins/errors/kind"
	"github.com/StevenACoffman/simplerr/errors"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type OperationMetadata struct {
	// At least one field in the operation has canary enabled.
	HasCanaryFields bool
	// At least one field in the operation has side-by-side enabled.
	HasSideBySideFields bool
	// Set if a selection set in the operation selects a field more than once
	// but doesn't alias one of the selections. This is valid, but gqlgen has a
	// bug related to "mixed aliases", so we need to know if operations include
	// them.
	//
	// Note(marksandstrom) This can be removed once we're using a version of
	// gqlgen that fixes https://github.com/99designs/gqlgen/issues/1271.
	HasMixedAliases bool
}

type _aliasFields struct {
	aliasFields    []string
	nonAliasFields []string
}

// MetadataForOperation extracts OperationMetadata for the given operation
// query text. This metadata is useful to prevent direct cross-service calls
// for operations that must go through the graphql-gateway for reasons other
// than the services that resolve the operations.
func MetadataForOperation(schema *ast.Schema, queryText string) (OperationMetadata, error) {
	query, errList := gqlparser.LoadQuery(schema, queryText)
	if errList != nil {
		return OperationMetadata{}, errList
	}
	if len(query.Operations) != 1 {
		return OperationMetadata{}, errors.Wrap(kind.Internal, "each query must contain exactly one operation")
	}
	operation := query.Operations[0]
	return processSelectionSetMetadata(operation.SelectionSet, new(_aliasFields)), nil
}

// selection set (including fields in fragments and inline fragments
// recursively).
func processSelectionSetMetadata(
	selectionSet ast.SelectionSet,
	aliasInfo *_aliasFields,
) OperationMetadata {
	var metadata OperationMetadata

	for _, selection := range selectionSet {
		switch v := selection.(type) {
		case *ast.Field:
			var isCanary bool
			var isSideBySide bool

			for _, directive := range v.Definition.Directives {
				if directive.Name == "migrate" {
					for _, argument := range directive.Arguments {
						if argument.Name == "state" {
							isCanary = argument.Value.Raw == "canary"
							isSideBySide = argument.Value.Raw == "side-by-side"
							break
						}
					}
				}
			}

			if v.Alias != v.Name {
				// Note: we want the name of the field, NOT the name of the
				// alias! We're concerned about selections like this:
				//
				// {
				//   aliasName: fieldName
				//   fieldName
				// }
				//
				// We want to detect if an alias is present and a field
				// selection without an alias is also present.
				aliasInfo.aliasFields = append(aliasInfo.aliasFields, v.Name)
			} else {
				aliasInfo.nonAliasFields = append(aliasInfo.nonAliasFields, v.Name)
			}

			// Each object selection should be analyzed separately for "mixed
			// aliases", so we create new alias info. Fragment alias info is
			// combined into the parent object selection info, so new info
			// isn't created for selections (see below).
			subselectionMetadata := processSelectionSetMetadata(v.SelectionSet, new(_aliasFields))

			metadata.HasSideBySideFields = isSideBySide ||
				metadata.HasSideBySideFields ||
				subselectionMetadata.HasSideBySideFields

			metadata.HasCanaryFields = metadata.HasCanaryFields ||
				isCanary ||
				subselectionMetadata.HasCanaryFields

			metadata.HasMixedAliases = metadata.HasMixedAliases ||
				subselectionMetadata.HasMixedAliases
		case *ast.FragmentSpread:
			processSelectionSetMetadata(v.Definition.SelectionSet, aliasInfo)
		case *ast.InlineFragment:
			processSelectionSetMetadata(v.SelectionSet, aliasInfo)
		}
	}

	metadata.HasMixedAliases = metadata.HasMixedAliases ||
		_hasCommonElement(aliasInfo.aliasFields, aliasInfo.nonAliasFields)

	return metadata
}

func _hasCommonElement(a, b []string) bool {
	valueInA := make(map[string]bool, len(a))

	for _, x := range a {
		valueInA[x] = true
	}

	for _, x := range b {
		if valueInA[x] {
			return true
		}
	}

	return false
}
