package baseapp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	dbm "github.com/tendermint/tm-db"

	pruningtypes "github.com/cosmos/cosmos-sdk/pruning/types"
	"github.com/cosmos/cosmos-sdk/snapshots"
	snapshottypes "github.com/cosmos/cosmos-sdk/snapshots/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestGetBlockRentionHeight(t *testing.T) {
	logger := defaultLogger()
	db := dbm.NewMemDB()
	name := t.Name()

	snapshotStore, err := snapshots.NewStore(dbm.NewMemDB(), testutil.GetTempDir(t))
	require.NoError(t, err)

	testCases := map[string]struct {
		bapp         *BaseApp
		maxAgeBlocks int64
		commitHeight int64
		expected     int64
	}{
		"defaults": {
			bapp:         NewBaseApp(name, logger, db, nil),
			maxAgeBlocks: 0,
			commitHeight: 499000,
			expected:     0,
		},
		"pruning unbonding time only": {
			bapp:         NewBaseApp(name, logger, db, nil, SetMinRetainBlocks(1)),
			maxAgeBlocks: 362880,
			commitHeight: 499000,
			expected:     136120,
		},
		"pruning iavl snapshot only": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetPruning(pruningtypes.NewPruningOptions(pruningtypes.PruningNothing)),
				SetMinRetainBlocks(1),
				SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(10000, 1)),
			),
			maxAgeBlocks: 0,
			commitHeight: 499000,
			expected:     489000,
		},
		"pruning state sync snapshot only": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(50000, 3)),
				SetMinRetainBlocks(1),
			),
			maxAgeBlocks: 0,
			commitHeight: 499000,
			expected:     349000,
		},
		"pruning min retention only": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetMinRetainBlocks(400000),
			),
			maxAgeBlocks: 0,
			commitHeight: 499000,
			expected:     99000,
		},
		"pruning all conditions": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetPruning(pruningtypes.NewCustomPruningOptions(0, 0)),
				SetMinRetainBlocks(400000),
				SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(50000, 3)),
			),
			maxAgeBlocks: 362880,
			commitHeight: 499000,
			expected:     99000,
		},
		"no pruning due to no persisted state": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetPruning(pruningtypes.NewCustomPruningOptions(0, 0)),
				SetMinRetainBlocks(400000),
				SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(50000, 3)),
			),
			maxAgeBlocks: 362880,
			commitHeight: 10000,
			expected:     0,
		},
		"disable pruning": {
			bapp: NewBaseApp(
				name, logger, db, nil,
				SetPruning(pruningtypes.NewCustomPruningOptions(0, 0)),
				SetMinRetainBlocks(0),
				SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(50000, 3)),
			),
			maxAgeBlocks: 362880,
			commitHeight: 499000,
			expected:     0,
		},
	}

	for name, tc := range testCases {
		tc := tc

		tc.bapp.SetParamStore(&paramStore{db: dbm.NewMemDB()})
		tc.bapp.InitChain(abci.RequestInitChain{
			ConsensusParams: &abci.ConsensusParams{
				Evidence: &tmproto.EvidenceParams{
					MaxAgeNumBlocks: tc.maxAgeBlocks,
				},
			},
		})

		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.bapp.GetBlockRetentionHeight(tc.commitHeight))
		})
	}
}

// Test and ensure that invalid block heights always cause errors.
// See issues:
// - https://github.com/cosmos/cosmos-sdk/issues/11220
// - https://github.com/cosmos/cosmos-sdk/issues/7662
func TestBaseAppCreateQueryContext(t *testing.T) {
	t.Parallel()

	logger := defaultLogger()
	db := dbm.NewMemDB()
	name := t.Name()
	app := NewBaseApp(name, logger, db, nil)

	app.BeginBlock(abci.RequestBeginBlock{Header: tmproto.Header{Height: 1}})
	app.Commit()

	app.BeginBlock(abci.RequestBeginBlock{Header: tmproto.Header{Height: 2}})
	app.Commit()

	testCases := []struct {
		name   string
		height int64
		prove  bool
		expErr bool
	}{
		{"valid height", 2, true, false},
		{"future height", 10, true, true},
		{"negative height, prove=true", -1, true, true},
		{"negative height, prove=false", -1, false, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.createQueryContext(tc.height, tc.prove)
			if tc.expErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBaseAppCreateQueryContextRejectsFutureHeights(t *testing.T) {
	t.Parallel()

	logger := defaultLogger()
	db := dbm.NewMemDB()
	name := t.Name()
	app := NewBaseApp(name, logger, db, nil)

	proves := []bool{
		false, true,
	}
	for _, prove := range proves {
		t.Run(fmt.Sprintf("prove=%t", prove), func(t *testing.T) {
			sctx, err := app.createQueryContext(30, true)
			require.Error(t, err)
			require.Equal(t, sctx, sdk.Context{})
		})
	}
}

type paramStore struct {
	db *dbm.MemDB
}

func (ps *paramStore) Set(_ sdk.Context, key []byte, value interface{}) {
	bz, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	ps.db.Set(key, bz)
}

func (ps *paramStore) Has(_ sdk.Context, key []byte) bool {
	ok, err := ps.db.Has(key)
	if err != nil {
		panic(err)
	}

	return ok
}

func (ps *paramStore) Get(_ sdk.Context, key []byte, ptr interface{}) {
	bz, err := ps.db.Get(key)
	if err != nil {
		panic(err)
	}

	if len(bz) == 0 {
		return
	}

	if err := json.Unmarshal(bz, ptr); err != nil {
		panic(err)
	}
}

func TestABCI_HaltChain(t *testing.T) {
	logger := defaultLogger()
	db := dbm.NewMemDB()
	name := t.Name()

	testCases := []struct {
		name        string
		haltHeight  uint64
		haltTime    uint64
		blockHeight int64
		blockTime   int64
		expHalt     bool
	}{
		{"default", 0, 0, 10, 0, false},
		{"halt-height-edge", 10, 0, 10, 0, false},
		{"halt-height", 10, 0, 11, 0, true},
		{"halt-time-edge", 0, 10, 1, 10, false},
		{"halt-time", 0, 10, 1, 11, true},
	}

	sigs := make(chan os.Signal, 5)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expHalt {
				signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			}

			defer func() {
				rec := recover()
				signal.Stop(sigs)
				var err error
				if rec != nil {
					err = rec.(error)
				}
				if !tc.expHalt {
					require.NoError(t, err)
				} else {
					// ensure that we received the correct signals
					require.Equal(t, syscall.SIGINT, <-sigs)
					require.Equal(t, syscall.SIGTERM, <-sigs)
					require.Equal(t, len(sigs), 0)

					// Check our error message.
					require.Error(t, err)
					require.True(t, strings.HasPrefix(err.Error(), "halt application"))
				}
			}()

			app := NewBaseApp(
				name, logger, db, nil,
				SetHaltHeight(tc.haltHeight),
				SetHaltTime(tc.haltTime),
			)

			app.InitChain(abci.RequestInitChain{
				InitialHeight: tc.blockHeight,
			})

			app.BeginBlock(abci.RequestBeginBlock{
				Header: tmproto.Header{
					Height: tc.blockHeight,
					Time:   time.Unix(tc.blockTime, 0),
				},
			})
		})
	}
}
