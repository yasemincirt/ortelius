// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm_index

const (
	BlockTypeProposal BlockType = iota
	BlockTypeAbort
	BlockTypeCommit
	BlockTypeStandard
	BlockTypeAtomic

	MaxBlockType = uint8(BlockTypeAtomic)
)

const (
	// Timed
	TransactionTypeAddDefaultSubnetValidator TransactionType = (iota * 2) + 11
	TransactionTypeAddNonDefaultSubnetValidator
	TransactionTypeAddDefaultSubnetDelegator

	// Decision
	TransactionTypeCreateChain
	TransactionTypeCreateSubnet

	// Atomic
	TransactionTypeImport
	TransactionTypeExport

	// Proposal but not Timed
	// TransactionTypeAdvanceTime
	TransactionTypeRewardValidator
)

type BlockType uint8
type TransactionType uint8

type Validator struct {
	TransactionType
}
