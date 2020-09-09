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
	"github.com/ava-labs/gecko/utils/hashing"
	"github.com/ava-labs/gecko/utils/wrappers"
	"github.com/ava-labs/gecko/vms/components/verify"
	"github.com/ava-labs/gecko/vms/platformvm"
	"github.com/ava-labs/gecko/vms/secp256k1fx"

	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/index"
)

var (
	ErrUnknownBlockType = errors.New("unknown block type")
	ErrUnknownTXType    = errors.New("unknown transaction type")
)

func (db *DB) Index(ctx context.Context, c services.Consumable) error {
	job := db.stream.NewJob("index")
	sess := db.db.NewSession(job)

	// Create db tx
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	// Consume the tx and commit
	err = db.indexBlock(services.NewConsumerContext(ctx, job, dbTx, c.Timestamp()), c.Body())
	if err != nil {
		return err
	}
	return dbTx.Commit()
}

func (db *DB) Bootstrap(ctx context.Context) error {
	pvmGenesisBytes, _, err := genesis.Genesis(db.networkID)
	if err != nil {
		return err
	}

	pvmGenesis := &platformvm.Genesis{}
	if err := db.codec.Unmarshal(pvmGenesisBytes, pvmGenesis); err != nil {
		panic(err)
		return err
	}

	if err = pvmGenesis.Initialize(); err != nil {
		return err
	}

	job := db.stream.NewJob("bootstrap")
	sess := db.db.NewSession(job)
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	cCtx := services.NewConsumerContext(ctx, job, dbTx, int64(pvmGenesis.Timestamp))
	blockID := ids.NewID([32]byte{})

	for _, tx := range append(pvmGenesis.Chains, pvmGenesis.Validators...) {
		if err = db.indexTx(cCtx, blockID, *tx); err != nil {
			return err
		}
	}

	return dbTx.Commit()
}

func (db *DB) indexBlock(ctx services.ConsumerCtx, blockBytes []byte) error {
	var block platformvm.Block
	if err := db.codec.Unmarshal(blockBytes, &block); err != nil {
		fmt.Println(blockBytes)
		panic(err)
		return ctx.Job().EventErr("index_block.unmarshal_block", err)
	}

	fmt.Println("blockBytes:")
	fmt.Println("block.Bytes():", block.Bytes())

	blkID := ids.NewID(hashing.ComputeHash256Array(blockBytes))

	// if len(block.Bytes()) == 0 {
	// 	panic("asdf")
	// }

	var (
		errs        = wrappers.Errs{}
		commonBlock platformvm.CommonBlock
		blockType   BlockType
	)
	switch blk := block.(type) {
	case *platformvm.StandardBlock:
		// blk.Initialize(&platformvm.VM{}, blockBytes)
		commonBlock, blockType = blk.CommonBlock, BlockTypeStandard
		for _, tx := range blk.Txs {
			errs.Add(db.indexTx(ctx, tx.ID(), *tx))
		}
	case *platformvm.ProposalBlock:
		commonBlock, blockType = blk.CommonBlock, BlockTypeProposal
		errs.Add(db.indexTx(ctx, blk.Tx.ID(), blk.Tx))
	case *platformvm.AtomicBlock:
		commonBlock, blockType = blk.CommonBlock, BlockTypeAtomic
		errs.Add(db.indexTx(ctx, blk.Tx.ID(), blk.Tx))
	case *platformvm.Abort:
		commonBlock, blockType = blk.CommonBlock, BlockTypeAbort
	case *platformvm.Commit:
		commonBlock, blockType = blk.CommonBlock, BlockTypeCommit
	default:
		return ctx.Job().EventErr("index_block", ErrUnknownBlockType)
	}

	_, err := ctx.DB().
		InsertInto("pvm_blocks").
		Pair("id", blkID.String()).
		Pair("type", blockType).
		Pair("parent_id", commonBlock.ParentID().String()).
		Pair("chain_id", db.chainID).
		Pair("serialization", blockBytes).
		Pair("created_at", ctx.Time()).
		ExecContext(ctx.Ctx())
	if err != nil && !index.IsDuplicateEntryError(err) {
		errs.Add(ctx.Job().EventErr("index_block.upsert_block", err))
	}
	return errs.Err
}

func (db *DB) indexTx(ctx services.ConsumerCtx, _ ids.ID, tx platformvm.Tx) error {
	var (
		errs   = wrappers.Errs{}
		baseTx *platformvm.BaseTx
		txType = index.TXTypeBase
	)

	txBytes, err := db.codec.Marshal(tx)
	if err != nil {
		panic(err)
	}
	fmt.Println("txBytes:", txBytes)

	txID := ids.NewID(hashing.ComputeHash256Array(txBytes))
	fmt.Println("tx id:", txID.String())

	switch typedTx := tx.UnsignedTx.(type) {
	case *platformvm.UnsignedAdvanceTimeTx:
		return nil
	case *platformvm.UnsignedCreateSubnetTx:
		txType = index.TXTypeCreateSubnet
		baseTx = &typedTx.BaseTx
		errs.Add(db.indexCreateSubnetTx(ctx, typedTx))
	case *platformvm.UnsignedCreateChainTx:
		txType = index.TXTypeCreateChain
		baseTx = &typedTx.BaseTx
		baseTxCredsLen := len(tx.Creds) - 1
		var subnetCred = verify.Verifiable(nil)
		if baseTxCredsLen >= 0 {
			subnetCred = tx.Creds[baseTxCredsLen]
			tx.Creds = tx.Creds[:baseTxCredsLen]
		}
		errs.Add(db.indexCreateChainTx(ctx, typedTx, subnetCred))

	case *platformvm.UnsignedImportTx:
		txType = index.TXTypeImport
		typedTx.BaseTx.Ins = append(typedTx.BaseTx.Ins, typedTx.ImportedInputs...)
		baseTx = &typedTx.BaseTx
	case *platformvm.UnsignedExportTx:
		txType = index.TXTypeExport
		typedTx.BaseTx.Outs = append(typedTx.BaseTx.Outs, typedTx.ExportedOutputs...)
		baseTx = &typedTx.BaseTx

	case *platformvm.UnsignedAddValidatorTx:
		txType = index.TXTypeAddValidator
		baseTx = &typedTx.BaseTx
		// errs.Add(db.indexValidator(ctx, typedTx))
	case *platformvm.UnsignedAddSubnetValidatorTx:
		txType = index.TXTypeAddSubnetValidator
		baseTx = &typedTx.BaseTx
		// errs.Add(db.indexValidator(ctx, typedTx))
	case *platformvm.UnsignedAddDelegatorTx:
		txType = index.TXTypeAddDelegator
		baseTx = &typedTx.BaseTx
		// errs.Add(db.indexValidator(ctx, typedTx))
	default:
		fmt.Println("$$$$$$$$$$$")
		fmt.Println(reflect.TypeOf(tx.UnsignedTx))
		return ctx.Job().EventErr("index_transaction", ErrUnknownTXType)
	}

	errs.Add(index.IngestBaseTx(ctx, txID, tx.UnsignedBytes(), &baseTx.BaseTx, txType, tx.Creds))

	return errs.Err
}

func (db *DB) indexCreateSubnetTx(ctx services.ConsumerCtx, tx *platformvm.UnsignedCreateSubnetTx) error {
	errs := wrappers.Errs{}

	// Add subnet
	builder := ctx.DB().
		InsertInto("pvm_subnets").
		Pair("id", tx.ID().String()).
		Pair("network_id", tx.NetworkID).
		Pair("chain_id", db.chainID).
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

func (db *DB) indexCreateChainTx(ctx services.ConsumerCtx, tx *platformvm.UnsignedCreateChainTx, creds verify.Verifiable) error {
	errs := wrappers.Errs{}

	_, err := ctx.DB().
		InsertInto("pvm_chains").
		Pair("id", tx.ID().String()).
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
			builder.Values(db.chainID, fxID.String())
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
				builder.Values(db.chainID, sig[:])
			}
			_, err = builder.ExecContext(ctx.Ctx())
			if err != nil && !index.IsDuplicateEntryError(err) {
				errs.Add(ctx.Job().EventErr("index_create_chain_tx.upsert_chain_control_sigs", err))
			}
		}
	}

	return errs.Err
}

func (db *DB) indexValidator(ctx services.ConsumerCtx, txID ids.ID, dv platformvm.Validator, destination ids.ShortID, shares uint32, subnetID ids.ID) error {
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
