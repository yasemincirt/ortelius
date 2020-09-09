// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package pvm

import (
	"context"

	"github.com/ava-labs/gecko/ids"
	"github.com/gocraft/dbr"

	"github.com/ava-labs/ortelius/services/index"
	"github.com/ava-labs/ortelius/services/models"
)

type sessionRunnerConstructor func(string) dbr.SessionRunner

type Readers struct {
	newSession sessionRunnerConstructor
}

func NewReaders(db *index.DB) *Readers {
	return &Readers{
		newSession: db.NewSession,
	}
}

func (r *Readers) ListBlocks(ctx context.Context, params ListBlocksParams) (*index.BlockList, error) {
	blocks := []*index.Block{}

	_, err := params.Apply(r.newSession("list_blocks").
		Select("id", "type", "parent_id", "chain_id", "created_at").
		From("pvm_blocks")).
		LoadContext(ctx, &blocks)

	if err != nil {
		return nil, err
	}
	return &index.BlockList{Blocks: blocks}, nil
}

func (r *Readers) ListSubnets(ctx context.Context, params ListSubnetsParams) (*index.SubnetList, error) {
	subnets := []*index.Subnet{}
	sess := r.newSession("list_subnets")
	_, err := params.Apply(sess.
		Select("id", "network_id", "threshold", "created_at").
		From("pvm_subnets")).
		LoadContext(ctx, &subnets)
	if err != nil {
		return nil, err
	}

	if err = loadControlKeys(ctx, sess, subnets); err != nil {
		return nil, err
	}

	return &index.SubnetList{Subnets: subnets}, nil
}

func (r *Readers) ListValidators(ctx context.Context, params ListValidatorsParams) (*index.ValidatorList, error) {
	validators := []*index.Validator{}

	_, err := params.Apply(r.newSession("list_blocks").
		Select("transaction_id", "node_id", "weight", "start_time", "end_time", "destination", "shares", "subnet_id").
		From("pvm_validators")).
		LoadContext(ctx, &validators)

	if err != nil {
		return nil, err
	}
	return &index.ValidatorList{Validators: validators}, nil
}

func (r *Readers) ListChains(ctx context.Context, params ListChainsParams) (*index.ChainList, error) {
	chains := []*index.Chain{}

	sess := r.newSession("list_chains")

	_, err := params.Apply(sess.
		Select("id", "network_id", "subnet_id", "name", "vm_id", "genesis_data", "created_at").
		From("pvm_chains")).
		LoadContext(ctx, &chains)
	if err != nil {
		return nil, err
	}

	if err = loadFXIDs(ctx, sess, chains); err != nil {
		return nil, err
	}
	if err = loadControlSignatures(ctx, sess, chains); err != nil {
		return nil, err
	}

	return &index.ChainList{Chains: chains}, nil
}

func (r *Readers) GetBlock(ctx context.Context, id ids.ID) (*index.Block, error) {
	list, err := r.ListBlocks(ctx, ListBlocksParams{ID: &id})
	if err != nil || len(list.Blocks) == 0 {
		return nil, err
	}
	return list.Blocks[0], nil
}

func (r *Readers) GetSubnet(ctx context.Context, id ids.ID) (*index.Subnet, error) {
	list, err := r.ListSubnets(ctx, ListSubnetsParams{ID: &id})
	if err != nil || len(list.Subnets) == 0 {
		return nil, err
	}
	return list.Subnets[0], nil
}

func (r *Readers) GetChain(ctx context.Context, id ids.ID) (*index.Chain, error) {
	list, err := r.ListChains(ctx, ListChainsParams{ID: &id})
	if err != nil || len(list.Chains) == 0 {
		return nil, err
	}
	return list.Chains[0], nil
}

func (r *Readers) GetValidator(ctx context.Context, id ids.ID) (*index.Validator, error) {
	list, err := r.ListValidators(ctx, ListValidatorsParams{ID: &id})
	if err != nil || len(list.Validators) == 0 {
		return nil, err
	}
	return list.Validators[0], nil
}

func loadControlKeys(ctx context.Context, sess dbr.SessionRunner, subnets []*index.Subnet) error {
	if len(subnets) < 1 {
		return nil
	}

	subnetMap := make(map[models.StringID]*index.Subnet, len(subnets))
	ids := make([]models.StringID, len(subnets))
	for i, s := range subnets {
		ids[i] = s.ID
		subnetMap[s.ID] = s
		s.ControlKeys = []index.ControlKey{}
	}

	keys := []struct {
		SubnetID models.StringID
		Key      index.ControlKey
	}{}
	_, err := sess.
		Select("subnet_id", "address", "public_key").
		From("pvm_subnet_control_keys").
		Where("pvm_subnet_control_keys.subnet_id IN ?", ids).
		LoadContext(ctx, &keys)
	if err != nil {
		return err
	}
	for _, key := range keys {
		s, ok := subnetMap[key.SubnetID]
		if ok {
			s.ControlKeys = append(s.ControlKeys, key.Key)
		}
	}

	return nil
}

func loadControlSignatures(ctx context.Context, sess dbr.SessionRunner, chains []*index.Chain) error {
	if len(chains) < 1 {
		return nil
	}

	chainMap := make(map[models.StringID]*index.Chain, len(chains))
	ids := make([]models.StringID, len(chains))
	for i, c := range chains {
		ids[i] = c.ID
		chainMap[c.ID] = c
		c.ControlSignatures = []index.ControlSignature{}
	}

	sigs := []struct {
		ChainID   models.StringID
		Signature index.ControlSignature
	}{}
	_, err := sess.
		Select("chain_id", "signature").
		From("pvm_chains_control_signatures").
		Where("pvm_chains_control_signatures.chain_id IN ?", ids).
		LoadContext(ctx, &sigs)
	if err != nil {
		return err
	}
	for _, sig := range sigs {
		s, ok := chainMap[sig.ChainID]
		if ok {
			s.ControlSignatures = append(s.ControlSignatures, sig.Signature)
		}
	}

	return nil
}

func loadFXIDs(ctx context.Context, sess dbr.SessionRunner, chains []*index.Chain) error {
	if len(chains) < 1 {
		return nil
	}

	chainMap := make(map[models.StringID]*index.Chain, len(chains))
	ids := make([]models.StringID, len(chains))
	for i, c := range chains {
		ids[i] = c.ID
		chainMap[c.ID] = c
		c.FxIDs = []models.StringID{}
	}

	fxIDs := []struct {
		ChainID models.StringID
		FXID    models.StringID `db:"fx_id"`
	}{}
	_, err := sess.
		Select("chain_id", "fx_id").
		From("pvm_chains_fx_ids").
		Where("pvm_chains_fx_ids.chain_id IN ?", ids).
		LoadContext(ctx, &fxIDs)
	if err != nil {
		return err
	}
	for _, fxID := range fxIDs {
		s, ok := chainMap[fxID.ChainID]
		if ok {
			s.FxIDs = append(s.FxIDs, fxID.FXID)
		}
	}

	return nil
}
