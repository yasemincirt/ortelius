// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm

import (
	"context"
	"errors"
	"strings"

	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/health"

	"github.com/ava-labs/ortelius/services"
	avaxIndexer "github.com/ava-labs/ortelius/services/indexes/avax"
	"github.com/ava-labs/ortelius/services/indexes/models"
)

var (
	ChainID = ids.NewID([32]byte{})

	ErrUnknownBlockType = errors.New("unknown block type")
)

type Writer struct {
	networkID uint32
	stream    *health.Stream
	db        *dbr.Connection
	avax      *avaxIndexer.Writer
}

func NewWriter(conns *services.Connections, networkID uint32) (*Writer, error) {
	return &Writer{
		networkID: networkID,
		stream:    conns.Stream(),
		db:        conns.DB(),
		avax:      avaxIndexer.NewWriter(ChainID.String(), conns.Stream()),
	}, nil
}

func (*Writer) Name() string { return "pvm-index" }

func (w *Writer) Close(_ context.Context) error {
	w.stream.Event("writer.close")
	return w.db.Close()
}

func (w *Writer) Consume(ctx context.Context, c services.Consumable) error {
	job := w.stream.NewJob("index")
	sess := w.db.NewSession(job)

	// Create w tx
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	// Consume the tx and commit
	err = w.indexBlock(services.NewConsumerContext(ctx, job, dbTx, c.Timestamp()), c.Body())
	if err != nil {
		return err
	}
	return dbTx.Commit()
}

func (w *Writer) Bootstrap(ctx context.Context) error {
	job := w.stream.NewJob("bootstrap")

	genesisBytes, _, err := genesis.Genesis(w.networkID)
	if err != nil {
		return err
	}

	platformGenesis := &platformvm.Genesis{}
	if err := platformvm.GenesisCodec.Unmarshal(genesisBytes, platformGenesis); err != nil {
		return err
	}
	if err = platformGenesis.Initialize(); err != nil {
		return err
	}

	var (
		db   = w.db.NewSession(job)
		errs = wrappers.Errs{}
		cCtx = services.NewConsumerContext(ctx, job, db, int64(platformGenesis.Timestamp))
	)

	for idx, utxo := range platformGenesis.UTXOs {
		select {
		case <-ctx.Done():
			break
		default:
		}

		errs.Add(w.avax.InsertOutput(cCtx, ChainID, uint32(idx), utxo.AssetID(), utxo.Out, false))
	}
	for _, tx := range append(platformGenesis.Validators, platformGenesis.Chains...) {
		select {
		case <-ctx.Done():
			break
		default:
		}

		errs.Add(w.indexTransaction(cCtx, ChainID, *tx))
	}

	return errs.Err
}

func (w *Writer) indexBlock(ctx services.ConsumerCtx, blockBytes []byte) error {
	var block platformvm.Block
	if err := platformvm.Codec.Unmarshal(blockBytes, &block); err != nil {
		return ctx.Job().EventErr("index_block.unmarshal_block", err)
	}

	errs := wrappers.Errs{}

	switch blk := block.(type) {
	case *platformvm.ProposalBlock:
		errs.Add(
			w.indexCommonBlock(ctx, models.BlockTypeProposal, blk.CommonBlock, blockBytes),
			w.indexTransaction(ctx, blk.ID(), blk.Tx),
		)
	case *platformvm.StandardBlock:
		errs.Add(w.indexCommonBlock(ctx, models.BlockTypeStandard, blk.CommonBlock, blockBytes))
		for _, tx := range blk.Txs {
			errs.Add(w.indexTransaction(ctx, blk.ID(), *tx))
		}
	case *platformvm.AtomicBlock:
		errs.Add(
			w.indexCommonBlock(ctx, models.BlockTypeProposal, blk.CommonBlock, blockBytes),
			w.indexTransaction(ctx, blk.ID(), blk.Tx),
		)
	case *platformvm.Abort:
		errs.Add(w.indexCommonBlock(ctx, models.BlockTypeAbort, blk.CommonBlock, blockBytes))
	case *platformvm.Commit:
		errs.Add(w.indexCommonBlock(ctx, models.BlockTypeCommit, blk.CommonBlock, blockBytes))
	default:
		ctx.Job().EventErr("index_block", ErrUnknownBlockType)
	}
	return nil
}

func (w *Writer) indexCommonBlock(ctx services.ConsumerCtx, blkType models.BlockType, blk platformvm.CommonBlock, blockBytes []byte) error {
	blkID := ids.NewID(hashing.ComputeHash256Array(blockBytes))

	_, err := ctx.DB().
		InsertInto("pvm_blocks").
		Pair("id", blkID.String()).
		Pair("type", blkType).
		Pair("parent_id", blk.ParentID().String()).
		Pair("serialization", blockBytes).
		Pair("created_at", ctx.Time()).
		ExecContext(ctx.Ctx())
	if err != nil && !errIsDuplicateEntryError(err) {
		return ctx.Job().EventErr("index_common_block.upsert_block", err)
	}
	return nil
}

func (w *Writer) indexTransaction(ctx services.ConsumerCtx, blockID ids.ID, tx platformvm.Tx) error {
	var (
		baseTx avax.BaseTx
		typ    models.TransactionType
	)

	switch castTx := tx.UnsignedTx.(type) {
	case *platformvm.UnsignedAddValidatorTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypeAddValidator
	case *platformvm.UnsignedAddSubnetValidatorTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypeAddSubnetValidator
	case *platformvm.UnsignedAddDelegatorTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypeAddDelegator
	case *platformvm.UnsignedCreateSubnetTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypeCreateSubnet
	case *platformvm.UnsignedCreateChainTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypeCreateChain
	case *platformvm.UnsignedImportTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypePVMImport
	case *platformvm.UnsignedExportTx:
		baseTx = castTx.BaseTx.BaseTx
		typ = models.TransactionTypePVMExport
	case *platformvm.UnsignedAdvanceTimeTx:
		typ = models.TransactionTypeAdvanceTime
	// baseTxs = append(baseTxs, castTx)
	case *platformvm.UnsignedRewardValidatorTx:
		typ = models.TransactionTypeRewardValidator
		// baseTxs = append(baseTxs, castTx.BaseTx.BaseTx)
	}
	return w.avax.InsertTransaction(ctx, tx.Bytes(), tx.UnsignedBytes(), &baseTx, tx.Creds, typ)
}

// func (w *Writer) indexCreateChainTx(ctx services.ConsumerCtx, blockID ids.ID, tx *platformvm.UnsignedCreateChainTx) error {
// 	txBytes, err := w.codec.Marshal(tx)
// 	if err != nil {
// 		return err
// 	}
//
// 	txID := ids.NewID(hashing.ComputeHash256Array(txBytes))
//
// 	err = w.indexTransactionOld(ctx, blockID, models.TransactionTypeCreateChain, txID)
// 	if err != nil {
// 		return err
// 	}
//
// 	// Add chain
// 	_, err = ctx.DB().
// 		InsertInto("pvm_chains").
// 		Pair("id", txID.String()).
// 		Pair("network_id", tx.NetworkID).
// 		Pair("subnet_id", tx.SubnetID.String()).
// 		Pair("name", tx.ChainName).
// 		Pair("vm_id", tx.VMID.String()).
// 		Pair("genesis_data", tx.GenesisData).
// 		ExecContext(ctx.Ctx())
// 	if err != nil && !errIsDuplicateEntryError(err) {
// 		return ctx.Job().EventErr("index_create_chain_tx.upsert_chain", err)
// 	}
//
// 	// Add FX IDs
// 	// if len(tx.ControlSigs) > 0 {
// 	// 	builder := ctx.DB().
// 	// 		InsertInto("pvm_chains_fx_ids").
// 	// 		Columns("chain_id", "fx_id")
// 	// 	for _, fxID := range tx.FxIDs {
// 	// 		builder.Values(w.chainID, fxID.String())
// 	// 	}
// 	// 	_, err = builder.ExecContext(ctx.Ctx())
// 	// 	if err != nil && !errIsDuplicateEntryError(err) {
// 	// 		return ctx.Job().EventErr("index_create_chain_tx.upsert_chain_fx_ids", err)
// 	// 	}
// 	// }
// 	//
// 	// // Add Control Sigs
// 	// if len(tx.ControlSigs) > 0 {
// 	// 	builder := ctx.DB().
// 	// 		InsertInto("pvm_chains_control_signatures").
// 	// 		Columns("chain_id", "signature")
// 	// 	for _, sig := range tx.ControlSigs {
// 	// 		builder.Values(w.chainID, sig[:])
// 	// 	}
// 	// 	_, err = builder.ExecContext(ctx.Ctx())
// 	// 	if err != nil && !errIsDuplicateEntryError(err) {
// 	// 		return ctx.Job().EventErr("index_create_chain_tx.upsert_chain_control_sigs", err)
// 	// 	}
// 	// }
// 	return nil
// }
//
// func (w *Writer) indexCreateSubnetTx(ctx services.ConsumerCtx, blockID ids.ID, tx *platformvm.UnsignedCreateSubnetTx) error {
// 	txBytes, err := w.codec.Marshal(tx)
// 	if err != nil {
// 		return err
// 	}
//
// 	txID := ids.NewID(hashing.ComputeHash256Array(txBytes))
//
// 	err = w.indexTransactionOld(ctx, blockID, models.TransactionTypeCreateSubnet, txID)
// 	if err != nil {
// 		return err
// 	}
//
// 	// Add subnet
// 	_, err = ctx.DB().
// 		InsertInto("pvm_subnets").
// 		Pair("id", txID.String()).
// 		Pair("network_id", tx.NetworkID).
// 		Pair("chain_id", w.chainID).
// 		// Pair("threshold", tx.Threshold).
// 		Pair("created_at", ctx.Time()).
// 		ExecContext(ctx.Ctx())
// 	if err != nil && !errIsDuplicateEntryError(err) {
// 		return ctx.Job().EventErr("index_create_subnet_tx.upsert_subnet", err)
// 	}
//
// 	// Add control keys
// 	// if len(tx.ControlKeys) > 0 {
// 	// 	builder := ctx.DB().
// 	// 		InsertInto("pvm_subnet_control_keys").
// 	// 		Columns("subnet_id", "address")
// 	// 	for _, address := range tx.ControlKeys {
// 	// 		builder.Values(txID.String(), address.String())
// 	// 	}
// 	// 	_, err = builder.ExecContext(ctx.Ctx())
// 	// 	if err != nil && !errIsDuplicateEntryError(err) {
// 	// 		return ctx.Job().EventErr("index_create_subnet_tx.upsert_control_keys", err)
// 	// 	}
// 	// }
//
// 	return nil
// }

// func (db *DB) indexValidator(ctx services.ConsumerCtx, txID ids.ID, dv platformvm.DurationValidator, destination ids.ShortID, shares uint32, subnetID ids.ID) error {
// 	_, err := ctx.DB().
// 		InsertInto("pvm_validators").
// 		Pair("transaction_id", txID.String()).
// 		Pair("node_id", dv.NodeID.String()).
// 		Pair("weight", dv.Weight()).
// 		Pair("start_time", dv.StartTime()).
// 		Pair("end_time", dv.EndTime()).
// 		Pair("destination", destination.String()).
// 		Pair("shares", shares).
// 		Pair("subnet_id", subnetID.String()).
// 		ExecContext(ctx.Ctx())
// 	if err != nil && !errIsDuplicateEntryError(err) {
// 		return ctx.Job().EventErr("index_validator.upsert_validator", err)
// 	}
// 	return nil
// }

func errIsDuplicateEntryError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry")
}
