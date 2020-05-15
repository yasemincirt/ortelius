// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm_index

import (
	"fmt"
	"strings"
	"time"

	"github.com/ava-labs/gecko/vms/platformvm"

	"github.com/ava-labs/ortelius/services"
)

func (db *DB) Index(i services.Indexable) error {
	job := db.stream.NewJob("index")
	sess := db.db.NewSession(job)

	// Create db tx
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}

	defer dbTx.RollbackUnlessCommitted()

	// Ingest the tx and commit
	err = db.index(services.NewIndexerContext(job, dbTx, i))
	if err != nil {
		return err
	}
	return dbTx.Commit()
}

func (db *DB) index(ctx services.IndexerCtx) error {

	// Parse the serialized fields from bytes into a new block
	var block platformvm.Block
	err := platformvm.Codec.Unmarshal(ctx.Body(), &block)
	if err != nil {
		return err
	}

	switch blk := block.(type) {
	// case *platformvm.ProposalBlock:
	// 	err = i.IngestBlockProposal(ctx, blk)
	// case *platformvm.Abort:
	// 	err = i.IngestBlockAbort(ctx, blk)
	// case *platformvm.Commit:
	// 	err = i.IngestBlockCommit(ctx, blk)
	case *platformvm.StandardBlock:
		err = db.IngestBlockStandard(ctx, blk)
	case *platformvm.AtomicBlock:
		err = db.IngestBlockAtomic(ctx, blk)
	}
	return err
}

//
// Transactions
//
func (db *DB) indexTransaction(ctx services.IndexerCtx, txType TransactionType) error {
	_, err := ctx.DB().
		InsertInto("pvm_transactions").
		Pair("id", ctx.ID().String()).
		Pair("chain_id", ctx.ChainID().String()).
		Pair("type", txType).
		Pair("created_at", ctx.Time()).
		Pair("canonical_serialization", ctx.Body()).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	return nil
}

func (db *DB) IngestTransactionTimed(ctx services.IndexerCtx, tTx platformvm.TimedTx) error {
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

func (db *DB) ingestAddDefaultSubnetValidatorTx(ctx services.IndexerCtx, tx *platformvm.AddDefaultSubnetValidatorTx) error {
	// db.indexValidator()\

	// # Default subnet validator
	// destination    binary(20)      not null,
	// 	shares         bigint unsigned not null,
	//
	// # Subnet validator
	// subnet_id      binary(32)    not null

	_, err := ctx.DB().
		InsertInto("pvm_validators").
		Pair("transaction_id", tx.ID()).
		Pair("node_id", tx.NodeID.String()).
		Pair("weight", tx.Weight()).
		Pair("start_time", tx.StartTime()).
		Pair("end_time", tx.EndTime()).
		Pair("end_time", tx.Destination.String()).
		Pair("end_time", tx.Shares).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}
	// tx.Wght
	// tx.NetworkID
	// tx.Destination
	// tx.NodeID
	// tx.Shares
	// tx.Start
	// tx.End
	// tx.Sig
	return db.indexTransaction(ctx, TransactionTypeAddDefaultSubnetValidator)
}

func (db *DB) ingestAddNonDefaultSubnetValidatorTx(ctx services.IndexerCtx, tx *platformvm.AddNonDefaultSubnetValidatorTx) error {
	return db.indexTransaction(ctx, TransactionTypeAddNonDefaultSubnetValidator)
}

func (db *DB) ingestAddDefaultSubnetDelegatorTx(ctx services.IndexerCtx, tx *platformvm.AddDefaultSubnsetDelegatorTx) error {
	return db.indexTransaction(ctx, TransactionTypeAddDefaultSubnetDelegator)
}

func (db *DB) indexValidator(ctx services.IndexerCtx, tx *platformvm.AddDefaultSubnsetDelegatorTx) error {
	return db.indexTransaction(ctx, TransactionTypeAddDefaultSubnetDelegator)
}

func (db *DB) IngestTransactionDecision(ctx services.IndexerCtx, dTx platformvm.DecisionTx) error {
	fmt.Println("IngestTransactionDecision:", dTx.ID())

	switch tx := dTx.(type) {
	case *platformvm.CreateChainTx:
		fmt.Println("IngestTransactionDecision CreateChainTx:", tx.ChainName)
	case *platformvm.CreateSubnetTx:
		fmt.Println("IngestTransactionDecision CreateChainTx:", tx.ControlKeys)
	}
	return nil
}

func (db *DB) IngestTransactionAtomic(ctx services.IndexerCtx, tx platformvm.AtomicTx) error {
	fmt.Println("IngestTransactionAtomic:", tx.ID())
	return nil
}

func (db *DB) IngestTransactionProposal(ctx services.IndexerCtx, tx platformvm.ProposalTx) error {
	// fmt.Println("IngestTransactionProposal:", tx.)
	return nil
}

//
// Blocks
//

func (db *DB) IngestBlockProposal(ctx services.IndexerCtx, blk *platformvm.ProposalBlock) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	if err := db.IngestTransactionProposal(blk.Tx); err != nil {
		return err
	}
	return nil
}

func (db *DB) IngestBlockAbort(ctx services.IndexerCtx, blk *platformvm.Abort) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockCommit(ctx services.IndexerCtx, blk *platformvm.Commit) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockStandard(ctx services.IndexerCtx, blk *platformvm.StandardBlock) error {
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

func (db *DB) IngestBlockAtomic(ctx services.IndexerCtx, blk *platformvm.AtomicBlock) error {
	if err := db.IngestBlockCommon(blk.CommonBlock); err != nil {
		return err
	}

	if err := db.IngestTransactionAtomic(blk.Tx); err != nil {
		return err
	}

	return nil
}

func (db *DB) IngestBlockCommon(ctx services.IndexerCtx, blk platformvm.CommonBlock) error {
	fmt.Println("IngestBlockCommon:", blk.ID())
	fmt.Println("IngestBlockCommon:", blk.ParentID())
	return nil
}

func errIsDuplicateEntryError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry")
}
