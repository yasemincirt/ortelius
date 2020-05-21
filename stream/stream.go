// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package stream

import (
	"errors"

	"github.com/ava-labs/gecko/ids"
)

var (
	ErrUnknownVM = errors.New("Unknown VM")
)

// Message is a message on the event stream
type Message struct {
	id        ids.ID
	chainID   ids.ID
	body      []byte
	timestamp int64
}

func (m *Message) ID() ids.ID       { return m.id }
func (m *Message) ChainID() ids.ID  { return m.chainID }
func (m *Message) Body() []byte     { return m.body }
func (m *Message) Timestamp() int64 { return m.timestamp }
