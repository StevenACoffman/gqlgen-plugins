package graphqltools

// This file contains types related to JSON serialization of operation services
// and metadata.

type OperationServices struct {
	From                string   `json:"from"`
	To                  []string `json:"to"`
	HasSideBySideFields bool     `json:"hasSideBySideFields"`
	HasCanaryFields     bool     `json:"hasCanaryFields"`
	HasMixedAliases     bool     `json:"hasMixedAliases"`
}
