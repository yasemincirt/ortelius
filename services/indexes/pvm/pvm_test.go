package pvm

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis"
	// "github.com/alicebob/miniredis"
	// "github.com/ava-labs/avalanche-go/vms/platformvm"
	"github.com/ava-labs/avalanchego/ids"

	"github.com/ava-labs/ortelius/cfg"
)

const testNetworkID = 0

func TestBootstrap(t *testing.T) {
	id, err := ids.FromString("11111111111111111111111111111111LpoYY")
	if err != nil {
		panic(err)
	}
	fmt.Println(id.Bytes())

	w, closeFn := newTestWriter(t)
	w.Bootstrap(context.Background())
	closeFn()
}

func newTestWriter(t *testing.T) (*Writer, func()) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatal("Failed to create miniredis server:", err.Error())
	}

	conf := cfg.Services{
		DB: &cfg.DB{
			TXDB:   true,
			Driver: "mysql",
			DSN:    "root:password@tcp(127.0.0.1:3306)/ortelius_test?parseTime=true",
		},
		Redis: &cfg.Redis{
			Addr: s.Addr(),
		},
	}

	w, err := NewWriter(conf, testNetworkID)
	if err != nil {
		t.Fatal("Failed to bootstrap index:", err.Error())
	}
	return w, func() {
		s.Close()
		w.Close(context.Background())
	}
}
