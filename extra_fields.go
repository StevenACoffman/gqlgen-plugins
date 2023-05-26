package gqlgen_plugins

// This file defines a wrapper for the ordinary modelgen plugin, which
// adds extra fields to models.  See WrapModelgenWithExtraFields,
// below, for details.

import (
	"go/types"
	"strings"

	"github.com/99designs/gqlgen/plugin"
	"github.com/99designs/gqlgen/plugin/modelgen"
)

// ExtraFieldConfig describes an extra field added to a GraphQL model -- see
// ExtraFields for details.
type ExtraFieldConfig struct {
	// Name is the Go name of the field.
	Name string `yaml:"name"`

	// Type is the Go type of the field.
	//
	// We support the builtin basic types (like string or int64), named types
	// (qualified by the full package path), pointers to those types (prefixed
	// with `*`), and slices of those types (prefixed with `[]`).
	//
	// For example, the following are valid types:
	//  string
	//  *github.com/Khan/webapp/pkg/web.Date
	//  []string
	//  []*github.com/Khan/webapp/pkg/web.Date
	//
	// TODO(benkraft): maps and other non-basic types, if we ever need them.
	//
	// Note that the type will be referenced from the generated/graphql, which
	// means the package it lives in must not reference the generated/graphql
	// package to avoid circular imports.
	// restrictions.
	Type string `yaml:"type"`

	// Description will be used as the doc-comment for the Go field.
	Description string `yaml:"description"`
}

// _namedType returns the specified named or builtin type.
//
// Note that we don't look up the full types.Type object from the appropriate
// package -- gqlgen doesn't give us the package-map we'd need to do so.
// Instead we construct a placeholder type that has all the fields gqlgen
// wants.  This is roughly what gqlgen itself does, anyway:
// https://github.com/99designs/gqlgen/blob/master/plugin/modelgen/models.go#L119
func _namedType(fullName string) types.Type {
	dotIndex := strings.LastIndex(fullName, ".")
	if dotIndex == -1 { // builtinType
		return types.Universe.Lookup(fullName).Type()
	}

	// type is pkg.Name
	pkgPath := fullName[:dotIndex]
	typeName := fullName[dotIndex+1:]

	pkgName := pkgPath
	slashIndex := strings.LastIndex(pkgPath, "/")
	if slashIndex != -1 {
		pkgName = pkgPath[slashIndex+1:]
	}

	pkg := types.NewPackage(pkgPath, pkgName)
	// gqlgen doesn't use some of the fields, so we leave them 0/nil
	return types.NewNamed(types.NewTypeName(0, pkg, typeName, nil), nil, nil)
}

// _buildType constructs a types.Type for the given string (using the syntax
// from ExtraFieldConfig.Type above).
func _buildType(typeString string) types.Type {
	switch {
	case typeString[0] == '*':
		return types.NewPointer(_buildType(typeString[1:]))
	case strings.HasPrefix(typeString, "[]"):
		return types.NewSlice(_buildType(typeString[2:]))
	default:
		return _namedType(typeString)
	}
}

// _makeExtraFieldsMutateHook returns a gqlgen MutateHook which adds extra
// fields described by WrapModelgenWithExtraFields to the GraphQL schema.
func _makeExtraFieldsMutateHook(
	cfg map[string][]ExtraFieldConfig,
	oldMutateHook modelgen.BuildMutateHook,
) func(*modelgen.ModelBuild) *modelgen.ModelBuild {
	return func(b *modelgen.ModelBuild) *modelgen.ModelBuild {
		// We apply upstream's mutate-hook, then add in ours.
		b = oldMutateHook(b)

		if len(cfg) == 0 {
			return b // no extra fields requested
		}

		for _, model := range b.Models {
			fieldConfigs, ok := cfg[model.Name]
			if !ok {
				continue // no modifications requested for this model
			}

			for _, fieldConfig := range fieldConfigs {
				model.Fields = append(model.Fields, &modelgen.Field{
					Name:        fieldConfig.Name,
					GoName:      fieldConfig.Name,
					Type:        _buildType(fieldConfig.Type),
					Tag:         `json:"-"`,
					Description: strings.TrimSpace(fieldConfig.Description),
				})
			}
		}
		return b
	}
}

// WrapModelgenWithExtraFields adds extra fields to the GraphQL model
// not exposed in the schema.
//
// These are useful for plumbing data from parent to child resolver, when
// there is no other good way to do so.  For example, if you have a query
// like
//
//	{ f(x: String!) { g } }
//
// and ResolveG needs access to the value of x, you might want to add a
// field for x to its return-type.  See ActivityLog in the progress service
// for an example.
//
// Using this functionality can make it harder to understand the flow of
// data, or to find bugs if the data is missing for some reason.  Don't use
// it if there's a good alternative!  For example, if two sibling resolvers
// need access to the result of the same expensive computation or datastore
// fetch, it's better to simply have them both call out to a function
// that's request-cached, rather than trying to have them coordinate as to
// whose job it is to populate the field when.
//
// Note that gqlgen also allows defining entirely custom models
// (https://gqlgen.com/reference/resolvers/) rather than using its
// autogenerated ones.  There are two disadvantages to this approach.  One
// is that it means we have to manually keep the models up to date if the
// schema changes.  (This may or may not be an issue in practice, depending
// on context.)  The second is that it means the models are spread out
// across multiple packages, since we put the generated code in a separate
// package.  This is only a minor confusion, but in some cases it can cause
// circular imports, which makes it a bigger problem.  So we offer adding
// custom fields to the autogenerated models as an alternative.
//
// See ExtraFieldConfig for configuration details.
func WrapModelgenWithExtraFields(
	cfg map[string][]ExtraFieldConfig,
) func(plugin.Plugin) plugin.Plugin {
	return func(p plugin.Plugin) plugin.Plugin {
		modelgenPlugin, _ := p.(*modelgen.Plugin)
		modelgenPlugin.MutateHook = _makeExtraFieldsMutateHook(
			cfg, modelgenPlugin.MutateHook)
		return modelgenPlugin
	}
}
