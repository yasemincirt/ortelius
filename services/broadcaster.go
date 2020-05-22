// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package services

import (
	"fmt"

	"github.com/ava-labs/ortelius/cfg"
)

type Broadcaster struct {
	chainConfig cfg.Chain
}

func NewBroadcaster(chainConfig cfg.Chain) *Broadcaster {
	return &Broadcaster{chainConfig: chainConfig}
}

func (*Broadcaster) Name() string { return "broadcaster" }

func (*Broadcaster) Bootstrap() error { return nil }

func (*Broadcaster) Consume(c Consumable) error {
	fmt.Println("Record", c.ChainID(), c.ID(), c.Timestamp())
	return nil
}
