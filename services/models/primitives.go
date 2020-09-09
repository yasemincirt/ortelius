// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package models

import (
	"encoding/json"
	"strconv"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/constants"
	"github.com/ava-labs/gecko/utils/formatting"
)

// StringID represents a 256bit hash encoded as a base58 string
type StringID string

// ToStringID converts an ids.ID into a StringID
func ToStringID(id ids.ID) StringID { return StringID(id.String()) }

// Equals returns true if and only if the two stringIDs represent the same ID
func (rid StringID) Equals(oRID StringID) bool { return string(rid) == string(oRID) }

// StringShortID represents a 160bit hash encoded as a base58 string
type StringShortID string

// ToShortStringID converts an ids.ShortID into a StringShortID
func ToShortStringID(id ids.ShortID) StringShortID {
	return StringShortID(id.String())
}

// Equals returns true if and only if the two stringShortIDs represent the same ID
func (rid StringShortID) Equals(oRID StringShortID) bool {
	return string(rid) == string(oRID)
}

var bech32HRP = constants.GetHRP(4)

type Address StringShortID

func (addr Address) MarshalJSON() ([]byte, error) {
	id, err := ids.ShortFromString(string(addr))
	if err != nil {
		return nil, err
	}

	bech32Addr, err := formatting.FormatBech32(bech32HRP, id.Bytes())
	if err != nil {
		return nil, err
	}

	return json.Marshal(bech32Addr)
}

// TransactionType represents a sub class of Transaction
type TransactionType string

// OutputType represents a sub class of Output
type OutputType uint32

// SearchResultType is the type for an object found from a search query.
type SearchResultType string

// AssetTokenCounts maps asset IDs to a TokenAmount for that asset
type AssetTokenCounts map[StringID]TokenAmount

type TokenAmount string

func TokenAmountForUint64(i uint64) TokenAmount {
	return TokenAmount(strconv.Itoa(int(i)))
}
