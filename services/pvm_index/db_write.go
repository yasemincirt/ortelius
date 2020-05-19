// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm_index

import (
	"strings"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/vms/platformvm"

	"github.com/ava-labs/ortelius/services"
)

func (db *DB) Index(i services.Indexable) error {
	job := db.stream.NewJob("indexBlock")
	sess := db.db.NewSession(job)

	// Create db tx
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}

	defer dbTx.RollbackUnlessCommitted()

	// Index the tx and commit
	err = db.indexBlock(services.NewIndexerContext(job, dbTx, i))
	if err != nil {
		return err
	}
	return dbTx.Commit()
}

//
// Blocks
//

func (db *DB) indexBlock(ctx services.IndexerCtx) error {
	// Parse the serialized fields from bytes into a new block
	var block platformvm.Block
	err := platformvm.Codec.Unmarshal(ctx.Body(), &block)
	if err != nil {
		return err
	}

	switch blk := block.(type) {
	// case *platformvm.Abort:
	// 	err = i.IndexBlockAbort(ctx, blk)
	// case *platformvm.Commit:
	// 	err = i.IndexBlockCommit(ctx, blk)
	case *platformvm.ProposalBlock:
		err = db.indexProposalBlock(ctx, blk)
	case *platformvm.StandardBlock:
		err = db.indexStandardBlock(ctx, blk)
	case *platformvm.AtomicBlock:
		err = db.indexAtomicBlock(ctx, blk)
	}
	return err
}

func (db *DB) indexProposalBlock(ctx services.IndexerCtx, blk *platformvm.ProposalBlock) error {
	if err := db.indexCommonBlock(ctx, blk.CommonBlock); err != nil {
		return err
	}

	if err := db.indexProposalTx(ctx, blk.Tx); err != nil {
		return err
	}
	return nil
}

func (db *DB) indexStandardBlock(ctx services.IndexerCtx, blk *platformvm.StandardBlock) error {
	err := db.indexCommonBlock(ctx, blk.CommonBlock)
	if err != nil {
		return err
	}

	for _, tx := range blk.Txs {
		if err = db.indexDecisionTx(ctx, tx); err != nil {
			return err
		}
	}

	return nil
}

func (db *DB) indexAtomicBlock(ctx services.IndexerCtx, blk *platformvm.AtomicBlock) error {
	if err := db.indexCommonBlock(ctx, blk.CommonBlock); err != nil {
		return err
	}

	if err := db.indexAtomicTx(ctx, blk.Tx); err != nil {
		return err
	}

	return nil
}

func (db *DB) indexCommonBlock(ctx services.IndexerCtx, blk platformvm.CommonBlock) error {
	return nil
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

//
// Decision Transactions
//

func (db *DB) indexDecisionTx(ctx services.IndexerCtx, dTx platformvm.DecisionTx) error {
	switch tx := dTx.(type) {
	case *platformvm.CreateChainTx:
		return db.indexCreateChainTx(ctx, tx)
	case *platformvm.CreateSubnetTx:
		return db.indexCreateSubnetTx(ctx, tx)
	}
	return nil
}

func (db *DB) indexCreateChainTx(ctx services.IndexerCtx, tx *platformvm.CreateChainTx) error {
	// Add transaction

	// Add chain
	_, err := ctx.DB().
		InsertInto("pvm_chains").
		Pair("id", ctx.ID()).
		Pair("network_id", tx.NetworkID).
		Pair("subnet_id", tx.SubnetID.String()).
		Pair("name", tx.ChainName).
		Pair("vm_id", tx.VMID.String()).
		Pair("genesis_data", tx.GenesisData).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	// Add FX IDs
	builder := ctx.DB().
		InsertInto("pvm_chains_fx_ids").
		Columns("chain_id", "fx_id")
	for _, fxID := range tx.FxIDs {
		builder.Values(ctx.ChainID().String, fxID.String())
	}
	_, err = builder.Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	// Add Control Sigs
	builder = ctx.DB().
		InsertInto("pvm_chains_control_signatures").
		Columns("chain_id", "signature")
	for _, sig := range tx.ControlSigs {
		builder.Values(ctx.ChainID().String, sig[:])
	}
	_, err = builder.Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	return nil
}

func (db *DB) indexCreateSubnetTx(ctx services.IndexerCtx, tx *platformvm.CreateSubnetTx) error {
	// Add transaction

	// Add subnet
	_, err := ctx.DB().
		InsertInto("pvm_subnets").
		Pair("id", ctx.ID()).
		Pair("network_id", tx.NetworkID).
		Pair("chain_id", ctx.ChainID().String()).
		Pair("threshold", tx.Threshold).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	// Add control keys
	builder := ctx.DB().
		InsertInto("pvm_subnet_control_keys").
		Columns("subnet_id", "address")
	for _, address := range tx.ControlKeys {
		builder.Values(ctx.ChainID().String, address.String())
	}
	_, err = builder.Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	return nil
}

//
// Proposal Transactions
//

func (db *DB) indexProposalTx(ctx services.IndexerCtx, pTx platformvm.ProposalTx) error {
	switch tx := pTx.(type) {
	case *platformvm.AdvanceTimeTx:
		return db.indexAdvanceTimeTx(ctx, tx)
	case *platformvm.RewardValidatorTx:
		return db.indexRewardValidatorTx(ctx, tx)
	case *platformvm.AddDefaultSubnetDelegatorTx:
		return db.indexAddDefaultSubnetDelegatorTx(ctx, tx)
	case *platformvm.AddDefaultSubnetValidatorTx:
		return db.indexAddDefaultSubnetValidatorTx(ctx, tx)
	case *platformvm.AddNonDefaultSubnetValidatorTx:
		return db.indexAddNonDefaultSubnetValidatorTx(ctx, tx)
	}
	return nil
}

func (db *DB) indexAddDefaultSubnetValidatorTx(ctx services.IndexerCtx, tx *platformvm.AddDefaultSubnetValidatorTx) error {
	err := db.indexValidator(ctx, tx.DurationValidator, tx.Destination, tx.Shares, ids.Empty)
	if err != nil {
		return err
	}

	return db.indexTransaction(ctx, TransactionTypeAddDefaultSubnetValidator)
}

func (db *DB) indexAddNonDefaultSubnetValidatorTx(ctx services.IndexerCtx, tx *platformvm.AddNonDefaultSubnetValidatorTx) error {
	err := db.indexValidator(ctx, tx.DurationValidator, ids.ShortEmpty, 0, tx.Subnet)
	if err != nil {
		return err
	}

	return db.indexTransaction(ctx, TransactionTypeAddNonDefaultSubnetValidator)
}

func (db *DB) indexAddDefaultSubnetDelegatorTx(ctx services.IndexerCtx, tx *platformvm.AddDefaultSubnetDelegatorTx) error {
	err := db.indexValidator(ctx, tx.DurationValidator, tx.Destination, platformvm.NumberOfShares, ids.Empty)
	if err != nil {
		return err
	}

	return db.indexTransaction(ctx, TransactionTypeAddDefaultSubnetDelegator)
}

func (db *DB) indexValidator(ctx services.IndexerCtx, dv platformvm.DurationValidator, destination ids.ShortID, shares uint32, subnetID ids.ID) error {
	_, err := ctx.DB().
		InsertInto("pvm_validators").
		Pair("transaction_id", ctx.ID()).
		Pair("node_id", dv.NodeID.String()).
		Pair("weight", dv.Weight()).
		Pair("start_time", dv.StartTime()).
		Pair("end_time", dv.EndTime()).
		Pair("destination", destination.String()).
		Pair("shares", shares).
		Pair("subnet_id", subnetID.String()).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}
	return nil
}

func (db *DB) indexAdvanceTimeTx(ctx services.IndexerCtx, tx *platformvm.AdvanceTimeTx) error {
	// Add transaction
	return nil
}

func (db *DB) indexRewardValidatorTx(ctx services.IndexerCtx, tx *platformvm.RewardValidatorTx) error {
	// Add transaction
	return nil
}

//
// Atomic Transactions
//

func (db *DB) indexAtomicTx(ctx services.IndexerCtx, tx platformvm.AtomicTx) error {
	return nil
}

func (db *DB) indexImportTx(ctx services.IndexerCtx, tx platformvm.ImportTx) error {
	return nil
}

func (db *DB) indexExportTx(ctx services.IndexerCtx, tx platformvm.ExportTx) error {
	return nil
}

func errIsDuplicateEntryError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry")
}
