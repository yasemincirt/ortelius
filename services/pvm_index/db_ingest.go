// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm_index

import (
	"fmt"

	"github.com/ava-labs/gecko/vms/platformvm"

	"github.com/ava-labs/ortelius/services"
)

func (db *DB) Ingest(ingestable services.Ingestable) error {
	// Parse the serialized fields from bytes into a new block
	var block platformvm.Block
	err := platformvm.Codec.Unmarshal(ingestable.Body(), &block)
	if err != nil {
		return err
	}

	switch blk := block.(type) {
	// case *platformvm.ProposalBlock:
	// 	err = i.IngestBlockProposal(blk)
	// case *platformvm.Abort:
	// 	err = i.IngestBlockAbort(blk)
	// case *platformvm.Commit:
	// 	err = i.IngestBlockCommit(blk)
	case *platformvm.StandardBlock:
		err = db.IngestBlockStandard(blk)
	case *platformvm.AtomicBlock:
		err = db.IngestBlockAtomic(blk)
	}
	return err
}

//
// Transactions
//
func (db *DB) IngestTransaction(ingestable services.Ingestable) error {
	return nil
}

func (db *DB) IngestTransactionTimed(tTx platformvm.TimedTx) error {
	fmt.Println("IngestTransactionTimed:", tTx.ID())

	switch tx := tTx.(type) {
	case *platformvm.AddDefaultSubnetValidatorTx:
		return db.ingestAddDefaultSubnetValidatorTx(tx)
	case *platformvm.AddNonDefaultSubnetValidatorTx:
		return db.ingestAddNonDefaultSubnetValidatorTx(tx)
	case *platformvm.AddDefaultSubnetDelegatorTx:
		return db.ingestAddDefaultSubnetDelegatorTx(tx)
	}

	return nil
}

func (db *DB) ingestAddDefaultSubnetValidatorTx(tx *platformvm.AddDefaultSubnetValidatorTx) error {
	fmt.Println("ingestAddDefaultSubnetValidatorTx:", tx.Wght)

	_, err = ctx.db.
		InsertInto("avm_transactions").
		Pair("id", baseTx.ID().String()).
		Pair("chain_id", baseTx.BCID.String()).
		Pair("type", TXTypeBase).
		Pair("created_at", ctx.time()).
		Pair("canonical_serialization", ctx.canonicalSerialization).
		Pair("json_serialization", ctx.jsonSerialization).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	// Proc

	return nil
}

func (db *DB) ingestAddNonDefaultSubnetValidatorTx(tx *platformvm.AddNonDefaultSubnetValidatorTx) error {
	fmt.Println("ingestAddNonDefaultSubnetValidatorTx:", tx.Wght)
	return nil
}

func (db *DB) ingestAddDefaultSubnetDelegatorTx(tx *platformvm.AddDefaultSubnetDelegatorTx) error {
	fmt.Println("ingestAddDefaultSubnetDelegatorTx:", tx.Wght)
	return nil
}

func (db *DB) IngestTransactionDecision(dTx platformvm.DecisionTx) error {
	fmt.Println("IngestTransactionDecision:", dTx.ID())

	switch tx := dTx.(type) {
	case *platformvm.CreateChainTx:
		fmt.Println("IngestTransactionDecision CreateChainTx:", tx.ChainName)
	case *platformvm.CreateSubnetTx:
		fmt.Println("IngestTransactionDecision CreateChainTx:", tx.ControlKeys)
	}
	return nil
}

func (db *DB) IngestTransactionAtomic(tx platformvm.AtomicTx) error {
	fmt.Println("IngestTransactionAtomic:", tx.ID())
	return nil
}

func (db *DB) IngestTransactionProposal(tx platformvm.ProposalTx) error {
	// fmt.Println("IngestTransactionProposal:", tx.)
	return nil
}

//
// Blocks
//

func (db *DB) IngestBlockProposal(blk *platformvm.ProposalBlock) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	if err := db.IngestTransactionProposal(blk.Tx); err != nil {
		return err
	}
	return nil
}

func (db *DB) IngestBlockAbort(blk *platformvm.Abort) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockCommit(blk *platformvm.Commit) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockStandard(blk *platformvm.StandardBlock) error {
	err := db.IngestBlockCommon(blk.CommonBlock)
	if err != nil {
		return err
	}

	for _, tx := range blk.Txs {
		if err = db.IngestTransactionDecision(tx); err != nil {
			return err
		}
	}

	return nil
}

func (db *DB) IngestBlockAtomic(blk *platformvm.AtomicBlock) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	if err := db.IngestTransactionAtomic(blk.Tx); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockCommon(blk platformvm.CommonBlock) error {
	fmt.Println("IngestBlockCommon:", blk.ID())
	fmt.Println("IngestBlockCommon:", blk.ParentID())
	return nil
}
