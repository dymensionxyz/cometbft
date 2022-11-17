package store

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/test"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmstore "github.com/tendermint/tendermint/proto/tendermint/store"
	tmversion "github.com/tendermint/tendermint/proto/tendermint/version"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"
	"github.com/tendermint/tendermint/version"
)

// A cleanupFunc cleans up any config / test files created for a particular
// test.
type cleanupFunc func()

// make an extended commit with a single vote containing just the height and a
// timestamp
func makeTestExtCommit(height int64, timestamp time.Time) *types.ExtendedCommit {
	extCommitSigs := []types.ExtendedCommitSig{{
		CommitSig: types.CommitSig{
			BlockIDFlag:      types.BlockIDFlagCommit,
			ValidatorAddress: tmrand.Bytes(crypto.AddressSize),
			Timestamp:        timestamp,
			Signature:        []byte("Signature"),
		},
		ExtensionSignature: []byte("ExtensionSignature"),
	}}
	return &types.ExtendedCommit{
		Height: height,
		BlockID: types.BlockID{
			Hash:          crypto.CRandBytes(32),
			PartSetHeader: types.PartSetHeader{Hash: crypto.CRandBytes(32), Total: 2},
		},
		ExtendedSignatures: extCommitSigs,
	}
}

func makeStateAndBlockStore(t *testing.T) (sm.State, dbm.DB, *BlockStore) {
	config := test.ResetTestRoot("blockchain_reactor_test")
	t.Cleanup(func() { os.RemoveAll(config.RootDir) })

	blockDB := dbm.NewMemDB()
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB, sm.StoreOptions{
		DiscardFinalizeBlockResponses: false,
	})
	state, err := stateStore.LoadFromDBOrGenesisFile(config.GenesisFile())
	if err != nil {
		panic(fmt.Errorf("error constructing state from genesis file: %w", err))
	}
	return state, blockDB, NewBlockStore(blockDB)
}

func TestLoadBlockStoreState(t *testing.T) {

	type blockStoreTest struct {
		testName string
		bss      *tmstore.BlockStoreState
		want     tmstore.BlockStoreState
	}

	testCases := []blockStoreTest{
		{"success", &tmstore.BlockStoreState{Base: 100, Height: 1000},
			tmstore.BlockStoreState{Base: 100, Height: 1000}},
		{"empty", &tmstore.BlockStoreState{}, tmstore.BlockStoreState{}},
		{"no base", &tmstore.BlockStoreState{Height: 1000}, tmstore.BlockStoreState{Base: 1, Height: 1000}},
	}

	for _, tc := range testCases {
		db := dbm.NewMemDB()
		batch := db.NewBatch()
		SaveBlockStoreState(batch, tc.bss)
		batch.WriteSync()
		batch.Close()
		retrBSJ := LoadBlockStoreState(db)
		assert.Equal(t, tc.want, retrBSJ, "expected the retrieved DBs to match: %s", tc.testName)
	}
}

func TestNewBlockStore(t *testing.T) {
	db := dbm.NewMemDB()
	bss := tmstore.BlockStoreState{Base: 100, Height: 10000}
	bz, _ := proto.Marshal(&bss)
	err := db.Set(blockStoreKey, bz)
	require.NoError(t, err)
	bs := NewBlockStore(db)
	require.Equal(t, int64(100), bs.Base(), "failed to properly parse blockstore")
	require.Equal(t, int64(10000), bs.Height(), "failed to properly parse blockstore")

	panicCausers := []struct {
		data    []byte
		wantErr string
	}{
		{[]byte("artful-doger"), "not unmarshal bytes"},
		{[]byte(" "), "unmarshal bytes"},
	}

	for i, tt := range panicCausers {
		tt := tt
		// Expecting a panic here on trying to parse an invalid blockStore
		_, _, panicErr := doFn(func() (interface{}, error) {
			err := db.Set(blockStoreKey, tt.data)
			require.NoError(t, err)
			_ = NewBlockStore(db)
			return nil, nil
		})
		require.NotNil(t, panicErr, "#%d panicCauser: %q expected a panic", i, tt.data)
		assert.Contains(t, fmt.Sprintf("%#v", panicErr), tt.wantErr, "#%d data: %q", i, tt.data)
	}

	err = db.Set(blockStoreKey, []byte{})
	require.NoError(t, err)
	bs = NewBlockStore(db)
	assert.Equal(t, bs.Height(), int64(0), "expecting empty bytes to be unmarshaled alright")
}

// var (
// 	state       sm.State
// 	block       *types.Block
// 	partSet     *types.PartSet
// 	part1       *types.Part
// 	part2       *types.Part
// 	seenCommit1 *types.Commit
// )

// func TestMain(m *testing.M) {
// 	var cleanup cleanupFunc
// 	var err error
// 	state, _, cleanup = makeStateAndBlockStore(log.NewTMLogger(new(bytes.Buffer)))
// 	block = state.MakeBlock(state.LastBlockHeight+1, test.MakeNTxs(state.LastBlockHeight+1, 10), new(types.Commit), nil, state.Validators.GetProposer().Address)

// 	partSet, err = block.MakePartSet(2)
// 	if err != nil {
// 		stdlog.Fatal(err)
// 	}
// 	part1 = partSet.GetPart(0)
// 	part2 = partSet.GetPart(1)
// 	seenCommit1 = makeTestExtCommit(10, tmtime.Now())
// 	code := m.Run()
// 	cleanup()
// 	os.Exit(code)
// }

// TODO: This test should be simplified ...

func TestBlockStoreSaveLoadBlock(t *testing.T) {
	state, _, bs := makeStateAndBlockStore(t)
	require.Equal(t, bs.Base(), int64(0), "initially the base should be zero")
	require.Equal(t, bs.Height(), int64(0), "initially the height should be zero")

	// check there are no blocks at various heights
	noBlockHeights := []int64{0, -1, 100, 1000, 2}
	for i, height := range noBlockHeights {
		if g := bs.LoadBlock(height); g != nil {
			t.Errorf("#%d: height(%d) got a block; want nil", i, height)
		}
	}

	// save a block
	block := state.MakeBlock(bs.Height()+1, nil, new(types.Commit), nil, state.Validators.GetProposer().Address)
	validPartSet, err := block.MakePartSet(2)
	require.NoError(t, err)
	seenCommit := makeTestExtCommit(1, tmtime.Now())
	bs.SaveBlockWithExtendedCommit(block, validPartSet, seenCommit)
	require.EqualValues(t, 1, bs.Base(), "expecting the new height to be changed")
	require.EqualValues(t, block.Header.Height, bs.Height(), "expecting the new height to be changed")

	incompletePartSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 2})
	uncontiguousPartSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 0})
	part2 := validPartSet.GetPart(1)
	_, err = uncontiguousPartSet.AddPart(part2)
	require.Error(t, err)

	header1 := types.Header{
		Version:         tmversion.Consensus{Block: version.BlockProtocol},
		Height:          1,
		ChainID:         "block_test",
		Time:            tmtime.Now(),
		ProposerAddress: tmrand.Bytes(crypto.AddressSize),
	}

	// End of setup, test data

	commitAtH10 := makeTestExtCommit(10, tmtime.Now()).ToCommit()
	tuples := []struct {
		block      *types.Block
		parts      *types.PartSet
		seenCommit *types.ExtendedCommit
		wantPanic  string
		wantErr    bool

		corruptBlockInDB      bool
		corruptCommitInDB     bool
		corruptSeenCommitInDB bool
		eraseCommitInDB       bool
		eraseSeenCommitInDB   bool
	}{
		{
			block:      newBlock(header1, commitAtH10),
			parts:      validPartSet,
			seenCommit: seenCommit,
		},

		{
			block:     nil,
			wantPanic: "only save a non-nil block",
		},

		{
			block: newBlock( // New block at height 5 in empty block store is fine
				types.Header{
					Version:         tmversion.Consensus{Block: version.BlockProtocol},
					Height:          5,
					ChainID:         "block_test",
					Time:            tmtime.Now(),
					ProposerAddress: tmrand.Bytes(crypto.AddressSize)},
				makeTestExtCommit(5, tmtime.Now()).ToCommit(),
			),
			parts:      validPartSet,
			seenCommit: makeTestExtCommit(5, tmtime.Now()),
		},

		{
			block:      newBlock(header1, commitAtH10),
			parts:      incompletePartSet,
			seenCommit: seenCommit,
			wantPanic:  "only save complete block", // incomplete parts
		},

		{
			block:             newBlock(header1, commitAtH10),
			parts:             validPartSet,
			seenCommit:        seenCommit,
			corruptCommitInDB: true, // Corrupt the DB's commit entry
			wantPanic:         "error reading block commit",
		},

		{
			block:            newBlock(header1, commitAtH10),
			parts:            validPartSet,
			seenCommit:       seenCommit,
			wantPanic:        "unmarshal to tmproto.BlockMeta",
			corruptBlockInDB: true, // Corrupt the DB's block entry
		},

		{
			block:      newBlock(header1, commitAtH10),
			parts:      validPartSet,
			seenCommit: seenCommit,

			// Expecting no error and we want a nil back
			eraseSeenCommitInDB: true,
		},

		{
			block:      newBlock(header1, commitAtH10),
			parts:      validPartSet,
			seenCommit: seenCommit,

			corruptSeenCommitInDB: true,
			wantPanic:             "error reading block seen commit",
		},

		{
			block:      newBlock(header1, commitAtH10),
			parts:      validPartSet,
			seenCommit: seenCommit,

			// Expecting no error and we want a nil back
			eraseCommitInDB: true,
		},
	}

	type quad struct {
		block  *types.Block
		commit *types.Commit
		meta   *types.BlockMeta

		seenCommit *types.Commit
	}

	for i, tuple := range tuples {
		tuple := tuple
		_, db, bs := makeStateAndBlockStore(t)
		// SaveBlock
		res, err, panicErr := doFn(func() (interface{}, error) {
			bs.SaveBlockWithExtendedCommit(tuple.block, tuple.parts, tuple.seenCommit)
			if tuple.block == nil {
				return nil, nil
			}

			if tuple.corruptBlockInDB {
				err := db.Set(calcBlockMetaKey(tuple.block.Height), []byte("block-bogus"))
				require.NoError(t, err)
			}
			bBlock := bs.LoadBlock(tuple.block.Height)
			bBlockMeta := bs.LoadBlockMeta(tuple.block.Height)

			if tuple.eraseSeenCommitInDB {
				err := db.Delete(calcSeenCommitKey(tuple.block.Height))
				require.NoError(t, err)
			}
			if tuple.corruptSeenCommitInDB {
				err := db.Set(calcSeenCommitKey(tuple.block.Height), []byte("bogus-seen-commit"))
				require.NoError(t, err)
			}
			bSeenCommit := bs.LoadSeenCommit(tuple.block.Height)

			commitHeight := tuple.block.Height - 1
			if tuple.eraseCommitInDB {
				err := db.Delete(calcBlockCommitKey(commitHeight))
				require.NoError(t, err)
			}
			if tuple.corruptCommitInDB {
				err := db.Set(calcBlockCommitKey(commitHeight), []byte("foo-bogus"))
				require.NoError(t, err)
			}
			bCommit := bs.LoadBlockCommit(commitHeight)
			return &quad{block: bBlock, seenCommit: bSeenCommit, commit: bCommit,
				meta: bBlockMeta}, nil
		})

		if subStr := tuple.wantPanic; subStr != "" {
			if panicErr == nil {
				t.Errorf("#%d: want a non-nil panic", i)
			} else if got := fmt.Sprintf("%#v", panicErr); !strings.Contains(got, subStr) {
				t.Errorf("#%d:\n\tgotErr: %q\nwant substring: %q", i, got, subStr)
			}
			continue
		}

		if tuple.wantErr {
			if err == nil {
				t.Errorf("#%d: got nil error", i)
			}
			continue
		}

		assert.Nil(t, panicErr, "#%d: unexpected panic", i)
		assert.Nil(t, err, "#%d: expecting a non-nil error", i)
		qua, ok := res.(*quad)
		if !ok || qua == nil {
			t.Errorf("#%d: got nil quad back; gotType=%T", i, res)
			continue
		}
		if tuple.eraseSeenCommitInDB {
			assert.Nil(t, qua.seenCommit,
				"erased the seenCommit in the DB hence we should get back a nil seenCommit")
		}
		if tuple.eraseCommitInDB {
			assert.Nil(t, qua.commit,
				"erased the commit in the DB hence we should get back a nil commit")
		}
	}
}

// TestSaveBlockWithExtendedCommitPanicOnAbsentExtension tests that saving a
// block with an extended commit panics when the extension data is absent.
func TestSaveBlockWithExtendedCommitPanicOnAbsentExtension(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		malleateCommit func(*types.ExtendedCommit)
		shouldPanic    bool
	}{
		{
			name:           "basic save",
			malleateCommit: func(_ *types.ExtendedCommit) {},
			shouldPanic:    false,
		},
		{
			name: "save commit with no extensions",
			malleateCommit: func(c *types.ExtendedCommit) {
				c.StripExtensions()
			},
			shouldPanic: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state, _, bs := makeStateAndBlockStore(t)
			block := test.MakeBlock(state)
			seenCommit := makeTestExtCommit(block.Header.Height, tmtime.Now())
			ps, err := block.MakePartSet(2)
			require.NoError(t, err)
			testCase.malleateCommit(seenCommit)
			if testCase.shouldPanic {
				require.Panics(t, func() {
					bs.SaveBlockWithExtendedCommit(block, ps, seenCommit)
				})
			} else {
				bs.SaveBlockWithExtendedCommit(block, ps, seenCommit)
			}
		})
	}
}

// TestLoadBlockExtendedCommit tests loading the extended commit for a previously
// saved block. The load method should return nil when only a commit was saved and
// return the extended commit otherwise.
func TestLoadBlockExtendedCommit(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		saveExtended bool
		expectResult bool
	}{
		{
			name:         "save commit",
			saveExtended: false,
			expectResult: false,
		},
		{
			name:         "save extended commit",
			saveExtended: true,
			expectResult: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state, _, bs := makeStateAndBlockStore(t)
			block := test.MakeBlock(state)
			seenCommit := makeTestExtCommit(block.Header.Height, tmtime.Now())
			ps, err := block.MakePartSet(2)
			require.NoError(t, err)
			if testCase.saveExtended {
				bs.SaveBlockWithExtendedCommit(block, ps, seenCommit)
			} else {
				bs.SaveBlock(block, ps, seenCommit.ToCommit())
			}
			res := bs.LoadBlockExtendedCommit(block.Height)
			if testCase.expectResult {
				require.Equal(t, seenCommit, res)
			} else {
				require.Nil(t, res)
			}
		})
	}
}

func TestLoadBaseMeta(t *testing.T) {
	config := test.ResetTestRoot("blockchain_reactor_test")
	defer os.RemoveAll(config.RootDir)
	stateStore := sm.NewStore(dbm.NewMemDB(), sm.StoreOptions{
		DiscardFinalizeBlockResponses: false,
	})
	state, err := stateStore.LoadFromDBOrGenesisFile(config.GenesisFile())
	require.NoError(t, err)
	bs := NewBlockStore(dbm.NewMemDB())

	for h := int64(1); h <= 10; h++ {
		block := state.MakeBlock(h, test.MakeNTxs(h, 10), new(types.Commit), nil, state.Validators.GetProposer().Address)
		partSet, err := block.MakePartSet(2)
		require.NoError(t, err)
		seenCommit := makeTestExtCommit(h, tmtime.Now())
		bs.SaveBlockWithExtendedCommit(block, partSet, seenCommit)
	}

	_, _, err = bs.PruneBlocks(4, state)
	require.NoError(t, err)

	baseBlock := bs.LoadBaseMeta()
	assert.EqualValues(t, 4, baseBlock.Header.Height)
	assert.EqualValues(t, 4, bs.Base())

	require.NoError(t, bs.DeleteLatestBlock())
	require.EqualValues(t, 9, bs.Height())
}

func TestLoadBlockPart(t *testing.T) {
	state, db, bs := makeStateAndBlockStore(t)
	height, index := int64(10), 1
	loadPart := func() (interface{}, error) {
		part := bs.LoadBlockPart(height, index)
		return part, nil
	}

	block := state.MakeBlock(state.LastBlockHeight+1, test.MakeNTxs(state.LastBlockHeight+1, 10), new(types.Commit), nil, state.Validators.GetProposer().Address)
	partSet, err := block.MakePartSet(2)
	require.NoError(t, err)
	part1 := partSet.GetPart(0)

	// Initially no contents.
	// 1. Requesting for a non-existent block shouldn't fail
	res, _, panicErr := doFn(loadPart)
	require.Nil(t, panicErr, "a non-existent block part shouldn't cause a panic")
	require.Nil(t, res, "a non-existent block part should return nil")

	// 2. Next save a corrupted block then try to load it
	err = db.Set(calcBlockPartKey(height, index), []byte("Tendermint"))
	require.NoError(t, err)
	res, _, panicErr = doFn(loadPart)
	require.NotNil(t, panicErr, "expecting a non-nil panic")
	require.Contains(t, panicErr.Error(), "unmarshal to tmproto.Part failed")

	// 3. A good block serialized and saved to the DB should be retrievable
	pb1, err := part1.ToProto()
	require.NoError(t, err)
	err = db.Set(calcBlockPartKey(height, index), mustEncode(pb1))
	require.NoError(t, err)
	gotPart, _, panicErr := doFn(loadPart)
	require.Nil(t, panicErr, "an existent and proper block should not panic")
	require.Nil(t, res, "a properly saved block should return a proper block")
	require.Equal(t, gotPart.(*types.Part), part1,
		"expecting successful retrieval of previously saved block")
}

func TestPruneBlocks(t *testing.T) {
	config := test.ResetTestRoot("blockchain_reactor_test")
	defer os.RemoveAll(config.RootDir)
	stateStore := sm.NewStore(dbm.NewMemDB(), sm.StoreOptions{
		DiscardFinalizeBlockResponses: false,
	})
	state, err := stateStore.LoadFromDBOrGenesisFile(config.GenesisFile())
	require.NoError(t, err)
	db := dbm.NewMemDB()
	bs := NewBlockStore(db)
	assert.EqualValues(t, 0, bs.Base())
	assert.EqualValues(t, 0, bs.Height())
	assert.EqualValues(t, 0, bs.Size())

	// pruning an empty store should error, even when pruning to 0
	_, _, err = bs.PruneBlocks(1, state)
	require.Error(t, err)

	_, _, err = bs.PruneBlocks(0, state)
	require.Error(t, err)

	// make more than 1000 blocks, to test batch deletions
	for h := int64(1); h <= 1500; h++ {
		block := state.MakeBlock(h, test.MakeNTxs(h, 10), new(types.Commit), nil, state.Validators.GetProposer().Address)
		partSet, err := block.MakePartSet(2)
		require.NoError(t, err)
		seenCommit := makeTestExtCommit(h, tmtime.Now())
		bs.SaveBlockWithExtendedCommit(block, partSet, seenCommit)
	}

	assert.EqualValues(t, 1, bs.Base())
	assert.EqualValues(t, 1500, bs.Height())
	assert.EqualValues(t, 1500, bs.Size())

	state.LastBlockTime = time.Date(2020, 1, 1, 1, 0, 0, 0, time.UTC)
	state.LastBlockHeight = 1500

	state.ConsensusParams.Evidence.MaxAgeNumBlocks = 400
	state.ConsensusParams.Evidence.MaxAgeDuration = 1 * time.Second

	// Check that basic pruning works
	pruned, evidenceRetainHeight, err := bs.PruneBlocks(1200, state)
	require.NoError(t, err)
	assert.EqualValues(t, 1199, pruned)
	assert.EqualValues(t, 1200, bs.Base())
	assert.EqualValues(t, 1500, bs.Height())
	assert.EqualValues(t, 301, bs.Size())
	assert.EqualValues(t, 1100, evidenceRetainHeight)

	require.NotNil(t, bs.LoadBlock(1200))
	require.Nil(t, bs.LoadBlock(1199))

	// The header and commit for heights 1100 onwards
	// need to remain to verify evidence
	require.NotNil(t, bs.LoadBlockMeta(1100))
	require.Nil(t, bs.LoadBlockMeta(1099))
	require.NotNil(t, bs.LoadBlockCommit(1100))
	require.Nil(t, bs.LoadBlockCommit(1099))

	for i := int64(1); i < 1200; i++ {
		require.Nil(t, bs.LoadBlock(i))
	}
	for i := int64(1200); i <= 1500; i++ {
		require.NotNil(t, bs.LoadBlock(i))
	}

	// Pruning below the current base should error
	_, _, err = bs.PruneBlocks(1199, state)
	require.Error(t, err)

	// Pruning to the current base should work
	pruned, _, err = bs.PruneBlocks(1200, state)
	require.NoError(t, err)
	assert.EqualValues(t, 0, pruned)

	// Pruning again should work
	pruned, _, err = bs.PruneBlocks(1300, state)
	require.NoError(t, err)
	assert.EqualValues(t, 100, pruned)
	assert.EqualValues(t, 1300, bs.Base())

	// we should still have the header and the commit
	// as they're needed for evidence
	require.NotNil(t, bs.LoadBlockMeta(1100))
	require.Nil(t, bs.LoadBlockMeta(1099))
	require.NotNil(t, bs.LoadBlockCommit(1100))
	require.Nil(t, bs.LoadBlockCommit(1099))

	// Pruning beyond the current height should error
	_, _, err = bs.PruneBlocks(1501, state)
	require.Error(t, err)

	// Pruning to the current height should work
	pruned, _, err = bs.PruneBlocks(1500, state)
	require.NoError(t, err)
	assert.EqualValues(t, 200, pruned)
	assert.Nil(t, bs.LoadBlock(1499))
	assert.NotNil(t, bs.LoadBlock(1500))
	assert.Nil(t, bs.LoadBlock(1501))
}

func TestLoadBlockMeta(t *testing.T) {
	_, db, bs := makeStateAndBlockStore(t)
	height := int64(10)
	loadMeta := func() (interface{}, error) {
		meta := bs.LoadBlockMeta(height)
		return meta, nil
	}

	// Initially no contents.
	// 1. Requesting for a non-existent blockMeta shouldn't fail
	res, _, panicErr := doFn(loadMeta)
	require.Nil(t, panicErr, "a non-existent blockMeta shouldn't cause a panic")
	require.Nil(t, res, "a non-existent blockMeta should return nil")

	// 2. Next save a corrupted blockMeta then try to load it
	err := db.Set(calcBlockMetaKey(height), []byte("Tendermint-Meta"))
	require.NoError(t, err)
	res, _, panicErr = doFn(loadMeta)
	require.NotNil(t, panicErr, "expecting a non-nil panic")
	require.Contains(t, panicErr.Error(), "unmarshal to tmproto.BlockMeta")

	// 3. A good blockMeta serialized and saved to the DB should be retrievable
	meta := &types.BlockMeta{Header: types.Header{
		Version: tmversion.Consensus{
			Block: version.BlockProtocol, App: 0}, Height: 1, ProposerAddress: tmrand.Bytes(crypto.AddressSize)}}
	pbm := meta.ToProto()
	err = db.Set(calcBlockMetaKey(height), mustEncode(pbm))
	require.NoError(t, err)
	gotMeta, _, panicErr := doFn(loadMeta)
	require.Nil(t, panicErr, "an existent and proper block should not panic")
	require.Nil(t, res, "a properly saved blockMeta should return a proper blocMeta ")
	pbmeta := meta.ToProto()
	if gmeta, ok := gotMeta.(*types.BlockMeta); ok {
		pbgotMeta := gmeta.ToProto()
		require.Equal(t, mustEncode(pbmeta), mustEncode(pbgotMeta),
			"expecting successful retrieval of previously saved blockMeta")
	}
}

func TestLoadBlockMetaByHash(t *testing.T) {
	config := test.ResetTestRoot("blockchain_reactor_test")
	defer os.RemoveAll(config.RootDir)
	stateStore := sm.NewStore(dbm.NewMemDB(), sm.StoreOptions{
		DiscardFinalizeBlockResponses: false,
	})
	state, err := stateStore.LoadFromDBOrGenesisFile(config.GenesisFile())
	require.NoError(t, err)
	bs := NewBlockStore(dbm.NewMemDB())

	b1 := state.MakeBlock(state.LastBlockHeight+1, test.MakeNTxs(state.LastBlockHeight+1, 10), new(types.Commit), nil, state.Validators.GetProposer().Address)
	partSet, err := b1.MakePartSet(2)
	require.NoError(t, err)
	seenCommit := makeTestExtCommit(1, tmtime.Now())
	bs.SaveBlockWithExtendedCommit(b1, partSet, seenCommit)

	baseBlock := bs.LoadBlockMetaByHash(b1.Hash())
	assert.EqualValues(t, b1.Header.Height, baseBlock.Header.Height)
	assert.EqualValues(t, b1.Header.LastBlockID, baseBlock.Header.LastBlockID)
	assert.EqualValues(t, b1.Header.ChainID, baseBlock.Header.ChainID)
}

func TestBlockFetchAtHeight(t *testing.T) {
	state, _, bs := makeStateAndBlockStore(t)
	require.Equal(t, bs.Height(), int64(0), "initially the height should be zero")
	block := state.MakeBlock(bs.Height()+1, nil, new(types.Commit), nil, state.Validators.GetProposer().Address)

	partSet, err := block.MakePartSet(2)
	require.NoError(t, err)
	seenCommit := makeTestExtCommit(block.Height, tmtime.Now())
	bs.SaveBlockWithExtendedCommit(block, partSet, seenCommit)
	require.Equal(t, bs.Height(), block.Header.Height, "expecting the new height to be changed")

	blockAtHeight := bs.LoadBlock(bs.Height())
	b1, err := block.ToProto()
	require.NoError(t, err)
	b2, err := blockAtHeight.ToProto()
	require.NoError(t, err)
	bz1 := mustEncode(b1)
	bz2 := mustEncode(b2)
	require.Equal(t, bz1, bz2)
	require.Equal(t, block.Hash(), blockAtHeight.Hash(),
		"expecting a successful load of the last saved block")

	blockAtHeightPlus1 := bs.LoadBlock(bs.Height() + 1)
	require.Nil(t, blockAtHeightPlus1, "expecting an unsuccessful load of Height()+1")
	blockAtHeightPlus2 := bs.LoadBlock(bs.Height() + 2)
	require.Nil(t, blockAtHeightPlus2, "expecting an unsuccessful load of Height()+2")
}

func doFn(fn func() (interface{}, error)) (res interface{}, err error, panicErr error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case error:
				panicErr = e
			case string:
				panicErr = fmt.Errorf("%s", e)
			default:
				if st, ok := r.(fmt.Stringer); ok {
					panicErr = fmt.Errorf("%s", st)
				} else {
					panicErr = fmt.Errorf("%s", debug.Stack())
				}
			}
		}
	}()

	res, err = fn()
	return res, err, panicErr
}

func newBlock(hdr types.Header, lastCommit *types.Commit) *types.Block {
	return &types.Block{
		Header:     hdr,
		LastCommit: lastCommit,
	}
}
