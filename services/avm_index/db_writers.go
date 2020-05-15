// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm_index

import (
	"errors"
	"strings"

	"github.com/ava-labs/gecko/genesis"
	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/crypto"
	"github.com/ava-labs/gecko/utils/hashing"
	"github.com/ava-labs/gecko/utils/math"
	"github.com/ava-labs/gecko/vms/avm"
	"github.com/ava-labs/gecko/vms/platformvm"
	"github.com/ava-labs/gecko/vms/secp256k1fx"
	"github.com/ava-labs/ortelius/services"
	"github.com/gocraft/dbr"
)

const (
	// MaxSerializationLen is the maximum number of bytes a canonically
	// serialized tx can be stored as in the database.
	MaxSerializationLen = 16_384
)

var (
	// ErrSerializationTooLong is returned when trying to ingest data with a
	// serialization larger than our max
	ErrSerializationTooLong = errors.New("serialization is too long")
)

func errIsDuplicateEntryError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry")
}

func (i *Index) Bootstrap() error {
	platformGenesisBytes, err := genesis.Genesis(i.networkID)
	if err != nil {
		return err
	}

	platformGenesis := &platformvm.Genesis{}
	if err = platformvm.Codec.Unmarshal(platformGenesisBytes, platformGenesis); err != nil {
		return err
	}
	if err = platformGenesis.Initialize(); err != nil {
		return err
	}

	avmGenesis := &avm.Genesis{}
	for _, chain := range platformGenesis.Chains {
		if chain.VMID.Equals(avm.ID) {
			if err := i.vm.Codec().Unmarshal(chain.GenesisData, avmGenesis); err != nil {
				return err
			}
			break
		}
	}

	for _, tx := range avmGenesis.Txs {
		txBytes, err := i.vm.Codec().Marshal(tx)
		if err != nil {
			return err
		}
		utx := &avm.UniqueTx{TxState: &avm.TxState{Tx: &avm.Tx{UnsignedTx: tx, Creds: nil}}}
		if err := i.db.Index(utx, platformGenesis.Timestamp, txBytes); err != nil {
			return err
		}
	}

	return nil
}

// AddTx ingests a Transaction and adds it to the services
func (r *DB) Index(tx *avm.UniqueTx, i services.Indexable) error {
	job := r.stream.NewJob("add_tx")
	sess := r.db.NewSession(job)

	// Create db tx
	dbTx, err := sess.Begin()
	if err != nil {
		return err
	}

	defer dbTx.RollbackUnlessCommitted()

	// Ingest the tx and commit
	err = r.ingestTx(services.NewIndexerContext(job, dbTx, i), tx)
	if err != nil {
		return err
	}
	return dbTx.Commit()
}

func (r *DB) ingestTx(ctx services.IndexerCtx, tx *avm.UniqueTx) error {
	// Validate that the serializations aren't too long
	if len(ctx.Body()) > MaxSerializationLen {
		return ErrSerializationTooLong
	}

	// Finish processing with a type-specific ingestion routine
	switch castTx := tx.UnsignedTx.(type) {
	case *avm.GenesisAsset:
		return r.ingestCreateAssetTx(ctx, &castTx.CreateAssetTx, castTx.Alias)
	case *avm.CreateAssetTx:
		return r.ingestCreateAssetTx(ctx, castTx, "")
	case *avm.OperationTx:
		// 	r.ingestOperationTx(ctx, tx)
	case *avm.BaseTx:
		return r.ingestBaseTx(ctx, tx, castTx)
	default:
		return errors.New("unknown tx type")
	}
	return nil
}

func (r *DB) ingestCreateAssetTx(ctx services.IndexerCtx, tx *avm.CreateAssetTx, alias string) error {
	wrappedTxBytes, err := r.codec.Marshal(&avm.Tx{UnsignedTx: tx})
	if err != nil {
		return err
	}
	txID := ids.NewID(hashing.ComputeHash256Array(wrappedTxBytes))

	var outputCount uint64
	var amount uint64
	for _, state := range tx.States {
		for _, out := range state.Outs {
			outputCount++

			xOut, ok := out.(*secp256k1fx.TransferOutput)
			if !ok {
				_ = ctx.Job().EventErr("assertion_to_secp256k1fx_transfer_output", errors.New("Output is not a *secp256k1fx.TransferOutput"))
				continue
			}

			r.ingestOutput(ctx, txID, outputCount-1, txID, xOut)

			amount, err = math.Add64(amount, xOut.Amount())
			if err != nil {
				_ = ctx.Job().EventErr("add_to_amount", err)
				continue
			}
		}
	}

	_, err = ctx.DB().
		InsertInto("avm_assets").
		Pair("id", txID.String()).
		Pair("chain_Id", r.chainID.String()).
		Pair("name", tx.Name).
		Pair("symbol", tx.Symbol).
		Pair("denomination", tx.Denomination).
		Pair("alias", alias).
		Pair("current_supply", amount).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	_, err = ctx.DB().
		InsertInto("avm_transactions").
		Pair("id", txID.String()).
		Pair("chain_id", r.chainID.String()).
		Pair("type", TXTypeCreateAsset).
		Pair("created_at", ctx.Time()).
		Pair("canonical_serialization", ctx.Serialization()).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}
	return nil
}

func (r *DB) ingestBaseTx(ctx services.IndexerCtx, uniqueTx *avm.UniqueTx, baseTx *avm.BaseTx) error {
	var (
		err   error
		total uint64 = 0
		creds        = uniqueTx.Credentials()
	)

	unsignedTxBytes, err := r.codec.Marshal(&uniqueTx.UnsignedTx)
	if err != nil {
		return err
	}

	var redeemOutputsConditions []dbr.Builder
	for i, in := range baseTx.Ins {
		total, err = math.Add64(total, in.Input().Amount())
		if err != nil {
			return err
		}

		inputID := in.TxID.Prefix(uint64(in.OutputIndex))

		redeemOutputsConditions = append(redeemOutputsConditions, dbr.Eq("id", inputID.String()))

		// Abort this iteration if no credentials were supplied
		if i > len(creds) {
			continue
		}

		// For each signature we recover the public key and the data to the db
		cred := creds[i].(*secp256k1fx.Credential)
		for _, sig := range cred.Sigs {
			publicKey, err := r.ecdsaRecoveryFactory.RecoverPublicKey(unsignedTxBytes, sig[:])
			if err != nil {
				return err
			}

			r.ingestAddressFromPublicKey(ctx, publicKey)
			r.ingestOutputAddress(ctx, inputID, publicKey.Address(), sig[:])
		}
	}

	if len(redeemOutputsConditions) > 0 {
		_, err = ctx.DB().
			Update("avm_outputs").
			Set("redeemed_at", dbr.Now).
			Set("redeeming_transaction_id", baseTx.ID().String()).
			Where(dbr.Or(redeemOutputsConditions...)).
			Exec()
		if err != nil {
			return err
		}
	}

	// Add baseTx to the table
	_, err = ctx.DB().
		InsertInto("avm_transactions").
		Pair("id", baseTx.ID().String()).
		Pair("chain_id", baseTx.BCID.String()).
		Pair("type", TXTypeBase).
		Pair("created_at", ctx.Time()).
		Pair("canonical_serialization", ctx.Serialization()).
		Pair("json_serialization", ctx.JSONSerialization()).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		return err
	}

	// Process baseTx outputs by adding to the outputs table
	for idx, out := range baseTx.Outs {
		xOut, ok := out.Output().(*secp256k1fx.TransferOutput)
		if !ok {
			continue
		}
		r.ingestOutput(ctx, baseTx.ID(), uint64(idx), out.AssetID(), xOut)
	}
	return nil
}

func (r *DB) ingestOutput(ctx services.IndexerCtx, txID ids.ID, idx uint64, assetID ids.ID, out *secp256k1fx.TransferOutput) {
	outputID := txID.Prefix(idx)

	_, err := ctx.DB().
		InsertInto("avm_outputs").
		Pair("id", outputID.String()).
		Pair("transaction_id", txID.String()).
		Pair("output_index", idx).
		Pair("asset_id", assetID.String()).
		Pair("output_type", OutputTypesSECP2556K1Transfer).
		Pair("amount", out.Amount()).
		Pair("created_at", ctx.Time()).
		Pair("locktime", out.Locktime).
		Pair("threshold", out.Threshold).
		Exec()
	if err != nil && !errIsDuplicateEntryError(err) {
		_ = r.stream.EventErr("ingest_output", err)
	}

	// Ingest each Output Address
	for _, addr := range out.Addresses() {
		addrBytes := [20]byte{}
		copy(addrBytes[:], addr)
		r.ingestOutputAddress(ctx, outputID, ids.NewShortID(addrBytes), nil)
	}
}

func (r *DB) ingestAddressFromPublicKey(ctx services.IndexerCtx, publicKey crypto.PublicKey) {
	_, err := ctx.DB().
		InsertInto("addresses").
		Pair("address", publicKey.Address().String()).
		Pair("public_key", publicKey.Bytes()).
		Exec()

	if err != nil && !errIsDuplicateEntryError(err) {
		_ = ctx.Job().EventErr("ingest_address_from_public_key", err)
	}
}

func (r *DB) ingestOutputAddress(ctx services.IndexerCtx, outputID ids.ID, address ids.ShortID, sig []byte) {
	builder := ctx.DB().
		InsertInto("avm_output_addresses").
		Pair("output_id", outputID.String()).
		Pair("address", address.String())

	if sig != nil {
		builder = builder.Pair("redeeming_signature", sig)
	}

	_, err := builder.Exec()
	switch {
	case err == nil:
		return
	case !errIsDuplicateEntryError(err):
		_ = ctx.Job().EventErr("ingest_output_address", err)
		return
	case sig == nil:
		return
	}

	_, err = ctx.DB().
		Update("avm_output_addresses").
		Set("redeeming_signature", sig).
		Where("output_id = ? and address = ?", outputID.String(), address.String()).
		Exec()
	if err != nil {
		_ = ctx.Job().EventErr("ingest_output_address", err)
		return
	}
}
