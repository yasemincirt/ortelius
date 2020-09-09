// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package index

import (
	"time"

	"github.com/ava-labs/ortelius/services/models"
)

var (
	OutputTypesSECP2556K1Transfer OutputType = 0x000000ff

	TXTypeBase               TransactionType = "base"
	TXTypeGenesisAsset       TransactionType = "genesis_asset"
	TXTypeCreateAsset        TransactionType = "create_asset"
	TXTypeImport             TransactionType = "import"
	TXTypeExport             TransactionType = "export"
	TXTypeOperation          TransactionType = "operation"
	TXTypeCreateSubnet       TransactionType = "create_subnet"
	TXTypeCreateChain        TransactionType = "create_chain"
	TXTypeAddValidator       TransactionType = "add_validator"
	TXTypeAddSubnetValidator TransactionType = "add_subnet_validator"
	TXTypeAddDelegator       TransactionType = "add_delegator"

	ResultTypeTransaction SearchResultType = "transaction"
	ResultTypeAsset       SearchResultType = "asset"
	ResultTypeAddress     SearchResultType = "address"
	ResultTypeOutput      SearchResultType = "output"
)

//
// Base models
//

type Transaction struct {
	ID      models.StringID `json:"id"`
	ChainID models.StringID `json:"chainID"`
	Type    string          `json:"type"`

	Inputs  []*Input  `json:"inputs"`
	Outputs []*Output `json:"outputs"`

	Memo []byte `json:"memo"`

	InputTotals         AssetTokenCounts `json:"inputTotals"`
	OutputTotals        AssetTokenCounts `json:"outputTotals"`
	ReusedAddressTotals AssetTokenCounts `json:"reusedAddressTotals"`

	CanonicalSerialization []byte    `json:"canonicalSerialization,omitempty"`
	CreatedAt              time.Time `json:"timestamp"`

	Score uint64 `json:"-"`
}

type Input struct {
	Output *Output            `json:"output"`
	Creds  []InputCredentials `json:"credentials"`
}

type Output struct {
	ID            models.StringID  `json:"id"`
	TransactionID models.StringID  `json:"transactionID"`
	OutputIndex   uint64           `json:"outputIndex"`
	AssetID       models.StringID  `json:"assetID"`
	OutputType    OutputType       `json:"outputType"`
	Amount        TokenAmount      `json:"amount"`
	Locktime      uint64           `json:"locktime"`
	Threshold     uint64           `json:"threshold"`
	Addresses     []models.Address `json:"addresses"`
	CreatedAt     time.Time        `json:"timestamp"`

	RedeemingTransactionID models.StringID `json:"redeemingTransactionID"`

	Score uint64 `json:"-"`
}

type InputCredentials struct {
	Address   models.Address `json:"address"`
	PublicKey []byte         `json:"public_key"`
	Signature []byte         `json:"signature"`
}

type OutputAddress struct {
	OutputID  models.StringID `json:"output_id"`
	Address   models.Address  `json:"address"`
	Signature []byte          `json:"signature"`
	CreatedAt time.Time       `json:"timestamp"`
	PublicKey []byte          `json:"-"`
}

type Asset struct {
	ID      models.StringID `json:"id"`
	ChainID models.StringID `json:"chainID"`

	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	Alias        string `json:"alias"`
	Denomination uint8  `json:"denomination"`

	CurrentSupply TokenAmount `json:"currentSupply"`
	CreatedAt     time.Time   `json:"timestamp"`

	Score uint64 `json:"-"`
}

type Address struct {
	Address   models.Address `json:"address"`
	PublicKey []byte         `json:"publicKey"`

	Assets map[models.StringID]AssetInfo `json:"assets"`

	Score uint64 `json:"-"`
}

type AssetInfo struct {
	AssetID models.StringID `json:"id"`

	TransactionCount uint64      `json:"transactionCount"`
	UTXOCount        uint64      `json:"utxoCount"`
	Balance          TokenAmount `json:"balance"`
	TotalReceived    TokenAmount `json:"totalReceived"`
	TotalSent        TokenAmount `json:"totalSent"`
}

//
// Lists
//

type ListMetadata struct {
	Count uint64 `json:"count"`
}

type TransactionList struct {
	ListMetadata
	Transactions []*Transaction `json:"transactions"`
}

type AssetList struct {
	ListMetadata
	Assets []*Asset `json:"assets"`
}

type AddressList struct {
	ListMetadata
	Addresses []*Address `json:"addresses"`
}

type OutputList struct {
	ListMetadata
	Outputs []*Output `json:"outputs"`
}

//
// Search
//

// SearchResults represents a set of items returned for a search query.
type SearchResults struct {
	// Count is the total number of matching results
	Count uint64 `json:"count"`

	// Results is a list of SearchResult
	Results SearchResultSet `json:"results"`
}

type SearchResultSet []SearchResult

func (s SearchResultSet) Len() int           { return len(s) }
func (s SearchResultSet) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s SearchResultSet) Less(i, j int) bool { return s[i].Score < s[j].Score }

// SearchResult represents a single item matching a search query.
type SearchResult struct {
	// SearchResultType is the type of object found
	SearchResultType `json:"type"`

	// Data is the object itself
	Data interface{} `json:"data"`

	// Score is a rank of how well this result matches the query
	Score uint64 `json:"score"`
}

//
// Aggregates
//

type AggregatesHistogram struct {
	Aggregates   Aggregates    `json:"aggregates"`
	IntervalSize time.Duration `json:"intervalSize,omitempty"`
	Intervals    []Aggregates  `json:"intervals,omitempty"`
}

type Aggregates struct {
	// Idx is used internally when creating a histogram of Aggregates.
	// It is exported only so it can be written to by dbr.
	Idx int `json:"-"`

	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`

	TransactionVolume TokenAmount `json:"transactionVolume"`

	TransactionCount uint64 `json:"transactionCount"`
	AddressCount     uint64 `json:"addressCount"`
	OutputCount      uint64 `json:"outputCount"`
	AssetCount       uint64 `json:"assetCount"`
}

const (
	BlockTypeProposal BlockType = iota
	BlockTypeAbort
	BlockTypeCommit
	BlockTypeStandard
	BlockTypeAtomic
)

type BlockType uint8

type Block struct {
	ID        models.StringID `json:"id"`
	ParentID  models.StringID `json:"parentID"`
	ChainID   models.StringID `json:"chainID"`
	Type      BlockType       `json:"type"`
	CreatedAt time.Time       `json:"createdAt"`
}

type BlockList struct {
	Blocks []*Block `json:"blocks"`
}

type Subnet struct {
	ID          models.StringID `json:"id"`
	Threshold   uint64          `json:"threshold"`
	ControlKeys []ControlKey    `json:"controlKeys"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type SubnetList struct {
	Subnets []*Subnet `json:"subnets"`
}

type Validator struct {
	TransactionID models.StringID `json:"transactionID"`

	NodeID models.StringShortID `json:"nodeID"`
	Weight string               `json:"weight"`

	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`

	Destination models.StringShortID `json:"destination"`
	Shares      uint32               `json:"shares"`

	SubnetID models.StringID `json:"subnetID"`
}

type ValidatorList struct {
	Validators []*Validator `json:"validators"`
}

type Chain struct {
	ID                models.StringID    `json:"id"`
	SubnetID          models.StringID    `json:"subnetID"`
	Name              string             `json:"name"`
	VMID              models.StringID    `json:"vmID" db:"vm_id"`
	ControlSignatures []ControlSignature `json:"controlSignatures"`
	FxIDs             []models.StringID  `json:"fxIDs"`
	GenesisData       []byte             `json:"genesisData"`
}

type ChainList struct {
	Chains []*Chain `json:"chains"`
}

type ControlKey struct {
	Address   models.StringShortID `json:"address"`
	PublicKey []byte               `json:"publicKey"`
}

type ControlSignature []byte
