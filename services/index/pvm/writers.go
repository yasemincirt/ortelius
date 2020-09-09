// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/ava-labs/gecko/genesis"
	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/codec"
	"github.com/ava-labs/gecko/utils/hashing"
	"github.com/ava-labs/gecko/utils/wrappers"
	"github.com/ava-labs/gecko/vms/components/verify"
	"github.com/ava-labs/gecko/vms/platformvm"
	"github.com/ava-labs/gecko/vms/secp256k1fx"
	"github.com/gocraft/health"

	"github.com/ava-labs/ortelius/cfg"
	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/index"
	"github.com/ava-labs/ortelius/services/models"
)

var (
	ErrUnknownBlockType = errors.New("unknown block type")
	ErrUnknownTXType    = errors.New("unknown transaction type")
)

type Writers struct {
	networkID uint32
	chainID   string

	db     *index.DB
	stream *health.Stream
	codec  codec.Codec
}

func NewWriters(conf cfg.Services, networkID uint32, chainID string) (*Writers, error) {
	conns, err := services.NewConnectionsFromConfig(conf)
	if err != nil {
		return nil, err
	}

	return &Writers{
		networkID: networkID,
		chainID:   chainID,
		db:        index.NewDB(conns.Stream(), conns.DB()),
		stream:    conns.Stream(),
		codec:     platformvm.Codec,
	}, nil
}

func (w *Writers) Name() string                    { return "pvm-index" }
func (w *Writers) Close(ctx context.Context) error { return w.db.Close(ctx) }

// Consume implements the Consumer interface
func (w *Writers) Consume(ctx context.Context, c services.Consumable) error {
	return w.Index(ctx, c)
}

func (w *Writers) Index(ctx context.Context, c services.Consumable) error {
	job := w.stream.NewJob("index")
	sess := w.db.NewSessionForEventReceiver(job)

	// Create db tx
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

func (w *Writers) Bootstrap(ctx context.Context) error {
	pvmGenesisBytes, _, err := genesis.Genesis(w.networkID)
	if err != nil {
		return err
	}

	pvmGenesis := &platformvm.Genesis{}
	if err := w.codec.Unmarshal(pvmGenesisBytes, pvmGenesis); err != nil {
		panic(err)
		return err
	}

	if err = pvmGenesis.Initialize(); err != nil {
		return err
	}

	job := w.stream.NewJob("bootstrap")
	sess := w.db.NewSessionForEventReceiver(job)
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	cCtx := services.NewConsumerContext(ctx, job, dbTx, int64(pvmGenesis.Timestamp))

	for _, tx := range append(pvmGenesis.Chains, pvmGenesis.Validators...) {
		if err = w.indexTx(cCtx, *tx); err != nil {
			return err
		}
	}

	return dbTx.Commit()
}

func (w *Writers) indexBlock(ctx services.ConsumerCtx, blockBytes []byte) error {
	var block platformvm.Block
	if err := w.codec.Unmarshal(blockBytes, &block); err != nil {
		return ctx.Job().EventErr("index_block.unmarshal_block", err)
	}

	blkID := ids.NewID(hashing.ComputeHash256Array(blockBytes))

	var (
		errs        = wrappers.Errs{}
		commonBlock platformvm.CommonBlock
		blockType   models.BlockType
	)
	switch blk := block.(type) {
	case *platformvm.StandardBlock:
		// blk.Initialize(&platformvm.VM{}, blockBytes)
		commonBlock, blockType = blk.CommonBlock, models.BlockTypeStandard
		for _, tx := range blk.Txs {
			errs.Add(w.indexTx(ctx, *tx))
		}
	case *platformvm.ProposalBlock:
		commonBlock, blockType = blk.CommonBlock, models.BlockTypeProposal
		errs.Add(w.indexTx(ctx, blk.Tx))
	case *platformvm.AtomicBlock:
		commonBlock, blockType = blk.CommonBlock, models.BlockTypeAtomic
		errs.Add(w.indexTx(ctx, blk.Tx))
	case *platformvm.Abort:
		commonBlock, blockType = blk.CommonBlock, models.BlockTypeAbort
	case *platformvm.Commit:
		commonBlock, blockType = blk.CommonBlock, models.BlockTypeCommit
	default:
		return ctx.Job().EventErr("index_block", ErrUnknownBlockType)
	}

	_, err := ctx.DB().
		InsertInto("pvm_blocks").
		Pair("id", blkID.String()).
		Pair("type", blockType).
		Pair("parent_id", commonBlock.ParentID().String()).
		Pair("chain_id", w.chainID).
		Pair("serialization", blockBytes).
		Pair("created_at", ctx.Time()).
		ExecContext(ctx.Ctx())
	if err != nil && !index.IsDuplicateEntryError(err) {
		errs.Add(ctx.Job().EventErr("index_block.upsert_block", err))
	}
	return errs.Err
}

func (w *Writers) indexTx(ctx services.ConsumerCtx, tx platformvm.Tx) error {
	var (
		errs   = wrappers.Errs{}
		baseTx *platformvm.BaseTx
		txType = models.TXTypeBase
	)

	txBytes, err := w.codec.Marshal(tx)
	if err != nil {
		return err
	}

	txID := ids.NewID(hashing.ComputeHash256Array(txBytes))

	switch typedTx := tx.UnsignedTx.(type) {
	case *platformvm.UnsignedAdvanceTimeTx:
		return nil
	case *platformvm.UnsignedCreateSubnetTx:
		txType = models.TXTypeCreateSubnet
		baseTx = &typedTx.BaseTx
		errs.Add(w.indexCreateSubnetTx(ctx, txID, typedTx))
	case *platformvm.UnsignedCreateChainTx:
		txType = models.TXTypeCreateChain
		baseTx = &typedTx.BaseTx
		baseTxCredsLen := len(tx.Creds) - 1
		var subnetCred = verify.Verifiable(nil)
		if baseTxCredsLen >= 0 {
			subnetCred = tx.Creds[baseTxCredsLen]
			tx.Creds = tx.Creds[:baseTxCredsLen]
		}
		errs.Add(w.indexCreateChainTx(ctx, txID, typedTx, subnetCred))

	case *platformvm.UnsignedImportTx:
		txType = models.TXTypeImport
		typedTx.BaseTx.Ins = append(typedTx.BaseTx.Ins, typedTx.ImportedInputs...)
		baseTx = &typedTx.BaseTx
	case *platformvm.UnsignedExportTx:
		txType = models.TXTypeExport
		typedTx.BaseTx.Outs = append(typedTx.BaseTx.Outs, typedTx.ExportedOutputs...)
		baseTx = &typedTx.BaseTx

	case *platformvm.UnsignedAddValidatorTx:
		txType = models.TXTypeAddValidator
		baseTx = &typedTx.BaseTx
		// errs.Add(w.indexValidator(ctx, txID, typedTx))
	case *platformvm.UnsignedAddSubnetValidatorTx:
		txType = models.TXTypeAddSubnetValidator
		baseTx = &typedTx.BaseTx
		// errs.Add(w.indexValidator(ctx, txID, typedTx))
	case *platformvm.UnsignedAddDelegatorTx:
		txType = models.TXTypeAddDelegator
		baseTx = &typedTx.BaseTx
		// errs.Add(w.indexValidator(ctx, txID, typedTx))
	default:
		fmt.Println("$$$$$$$$$$$")
		fmt.Println(reflect.TypeOf(tx.UnsignedTx))
		return ctx.Job().EventErr("index_transaction", ErrUnknownTXType)
	}

	errs.Add(index.IngestBaseTx(ctx, txID, tx.UnsignedBytes(), &baseTx.BaseTx, txType, tx.Creds))

	return errs.Err
}

func (w *Writers) indexCreateSubnetTx(ctx services.ConsumerCtx, txID ids.ID, tx *platformvm.UnsignedCreateSubnetTx) error {
	errs := wrappers.Errs{}

	// Add subnet
	builder := ctx.DB().
		InsertInto("pvm_subnets").
		Pair("id", txID.String()).
		Pair("network_id", tx.NetworkID).
		Pair("chain_id", w.chainID).
		Pair("created_at", ctx.Time())

	// Add owner
	switch owner := tx.Owner.(type) {
	case *secp256k1fx.OutputOwners:
		builder.Pair("threshold", owner.Threshold)
		builder.Pair("locktime", owner.Locktime)

		if len(owner.Addrs) > 0 {
			builder := ctx.DB().
				InsertInto("pvm_subnet_control_keys").
				Columns("subnet_id", "address")
			for _, address := range owner.Addrs {
				builder.Values(tx.ID().String(), address.String())
			}
			if _, err := builder.ExecContext(ctx.Ctx()); err != nil && !index.IsDuplicateEntryError(err) {
				errs.Add(ctx.Job().EventErr("index_create_subnet_tx.upsert_control_keys", err))
			}
		}

	}

	if _, err := builder.ExecContext(ctx.Ctx()); err != nil && !index.IsDuplicateEntryError(err) {
		errs.Add(ctx.Job().EventErr("index_create_subnet_tx.upsert_subnet", err))
	}

	return errs.Err
}

func (w *Writers) indexCreateChainTx(ctx services.ConsumerCtx, txID ids.ID, tx *platformvm.UnsignedCreateChainTx, creds verify.Verifiable) error {
	errs := wrappers.Errs{}

	_, err := ctx.DB().
		InsertInto("pvm_chains").
		Pair("id", txID.String()).
		Pair("network_id", tx.NetworkID).
		Pair("subnet_id", tx.SubnetID.String()).
		Pair("name", tx.ChainName).
		Pair("vm_id", tx.VMID.String()).
		Pair("genesis_data", tx.GenesisData).
		ExecContext(ctx.Ctx())
	if err != nil && !index.IsDuplicateEntryError(err) {
		errs.Add(ctx.Job().EventErr("index_create_chain_tx.upsert_chain", err))
	}

	// Add feature extentions
	if len(tx.FxIDs) > 0 {
		builder := ctx.DB().
			InsertInto("pvm_chains_fx_ids").
			Columns("chain_id", "fx_id")
		for _, fxID := range tx.FxIDs {
			builder.Values(w.chainID, fxID.String())
		}

		if _, err = builder.ExecContext(ctx.Ctx()); err != nil && !index.IsDuplicateEntryError(err) {
			errs.Add(ctx.Job().EventErr("index_create_chain_tx.upsert_chain_fx_ids", err))
		}
	}

	// Add control signatures
	switch auth := creds.(type) {
	case *secp256k1fx.Credential:
		if len(auth.Sigs) > 0 {
			builder := ctx.DB().
				InsertInto("pvm_chains_control_signatures").
				Columns("chain_id", "signature")
			for _, sig := range auth.Sigs {
				builder.Values(w.chainID, sig[:])
			}
			_, err = builder.ExecContext(ctx.Ctx())
			if err != nil && !index.IsDuplicateEntryError(err) {
				errs.Add(ctx.Job().EventErr("index_create_chain_tx.upsert_chain_control_sigs", err))
			}
		}
	}

	return errs.Err
}

func (w *Writers) indexValidator(ctx services.ConsumerCtx, txID ids.ID, dv platformvm.Validator, destination ids.ShortID, shares uint32, subnetID ids.ID) error {
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
	// 	if err != nil && !index.IsDuplicateEntryError(err) {
	// 		return ctx.Job().EventErr("index_validator.upsert_validator", err)
	// 	}
	return nil
}
