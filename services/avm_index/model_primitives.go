// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm_index

import "github.com/ava-labs/gecko/ids"

type tokenAmount string

// TransactionType represents a sub class of Transaction
type TransactionType string

// OutputType represents a sub class of Output
type OutputType uint32

// SearchResultType is the type for an object found from a search query.
type SearchResultType string

// AssetTokenCounts maps asset IDs to a tokenAmount for that asset
type AssetTokenCounts map[stringID]tokenAmount
