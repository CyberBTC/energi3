// Copyright 2019 The Energi Core Authors
// This file is part of the Energi Core library.
//
// The Energi Core library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Energi Core library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Energi Core library. If not, see <http://www.gnu.org/licenses/>.

package consensus

import (
	"crypto/ecdsa"
	"crypto/rand"
	"math/big"
	"testing"

	"energi.world/core/gen3/common"
	eth_consensus "energi.world/core/gen3/consensus"
	"energi.world/core/gen3/core"
	"energi.world/core/gen3/core/types"
	"energi.world/core/gen3/core/vm"
	"energi.world/core/gen3/crypto"
	"energi.world/core/gen3/ethdb"
	"energi.world/core/gen3/log"
	"energi.world/core/gen3/params"

	"github.com/stretchr/testify/assert"

	energi_params "energi.world/core/gen3/energi/params"
)

func TestPoSChain(t *testing.T) {
	t.Parallel()
	log.Root().SetHandler(log.StdoutHandler)

	results := make(chan *eth_consensus.SealResult, 1)
	stop := make(chan struct{})

	signers := make(map[common.Address]*ecdsa.PrivateKey, 61)
	addresses := make([]common.Address, 0, 60)
	alloc := make(core.GenesisAlloc, 61)
	for i := 0; i < 60; i++ {
		k, _ := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
		a := crypto.PubkeyToAddress(k.PublicKey)

		signers[a] = k
		addresses = append(addresses, a)
		alloc[a] = core.GenesisAccount{
			Balance: minStake,
		}
	}
	alloc[energi_params.Energi_MigrationContract] = core.GenesisAccount{
		Balance: minStake,
	}
	migrationSigner := addresses[59]
	signers[energi_params.Energi_MigrationContract] = signers[migrationSigner]

	testdb := ethdb.NewMemDatabase()
	engine := New(&params.EnergiConfig{MigrationSigner: migrationSigner}, testdb)
	var header *types.Header

	engine.testing = true
	engine.SetMinerCB(
		func() []common.Address {
			if header.Number.Uint64() == 1 {
				return []common.Address{
					energi_params.Energi_MigrationContract,
				}
			}

			return addresses
		},
		func(addr common.Address, hash []byte) ([]byte, error) {
			return crypto.Sign(hash, signers[addr])
		},
		func() int { return 1 },
	)

	chainConfig := *params.EnergiTestnetChainConfig
	chainConfig.Energi = &params.EnergiConfig{
		MigrationSigner: migrationSigner,
	}

	var (
		gspec = &core.Genesis{
			Config:     &chainConfig,
			GasLimit:   8000000,
			Timestamp:  1000,
			Difficulty: big.NewInt(1),
			Coinbase:   energi_params.Energi_Treasury,
			Alloc:      alloc,
			Xfers:      core.DeployEnergiGovernance(&chainConfig),
		}
		genesis = gspec.MustCommit(testdb)

		now = engine.now()
	)

	chain, err := core.NewBlockChain(testdb, nil, &chainConfig, engine, vm.Config{}, nil)
	assert.Empty(t, err)
	defer chain.Stop()

	//--
	_, err = chain.InsertChain([]*types.Block{genesis})
	assert.Empty(t, err)

	parent := chain.GetHeaderByHash(genesis.Hash())
	assert.NotEmpty(t, parent)

	iterCount := 150
	//iterMid := iterCount * 2 / 3

	engine.diffFn = func(ChainReader, uint64, *types.Header, *timeTarget) *big.Int {
		return common.Big1
	}

	for i := 1; i < iterCount; i++ {
		number := new(big.Int).Add(parent.Number, common.Big1)

		//---
		header = &types.Header{
			ParentHash: parent.Hash(),
			Coinbase:   common.Address{},
			GasLimit:   parent.GasLimit,
			Number:     number,
			Time:       parent.Time,
		}
		blstate := chain.CalculateBlockState(header.ParentHash, parent.Number.Uint64())
		assert.NotEmpty(t, blstate)

		err = engine.Prepare(chain, header)
		assert.Empty(t, err)
		assert.NotEmpty(t, header.Difficulty)
		txs := types.Transactions{}
		receipts := []*types.Receipt{}
		if i == 1 {
			tx := migrationTx(
				types.NewEIP155Signer(chainConfig.ChainID), header,
				&snapshot{
					Txouts: []snapshotItem{
						{
							Owner:  "t6vtJKxdjaJdofaUrx7w4xUs5bMcjDq5R2",
							Amount: big.NewInt(10228000000),
							Atype:  "pubkeyhash",
						},
					},
				}, engine)
			receipt, _, err := core.ApplyTransaction(
				&chainConfig, chain, &header.Coinbase,
				new(core.GasPool).AddGas(header.GasLimit),
				blstate, header, tx,
				&header.GasUsed, *chain.GetVMConfig())
			assert.Empty(t, err)
			txs = append(txs, tx)
			receipts = append(receipts, receipt)
		}
		block, receipts, err := engine.Finalize(
			chain, header, blstate, txs, nil, receipts)
		assert.Empty(t, err)

		if i == 1 {
			assert.Equal(t, 1, len(receipts))
		} else {
			assert.Empty(t, receipts)
		}

		//---
		err = engine.Seal(chain, block, results, stop)
		assert.Empty(t, err)

		seal_res := <-results
		block = seal_res.Block
		blstate = seal_res.NewState
		receipts = seal_res.Receipts
		assert.NotEmpty(t, block)
		assert.NotEmpty(t, blstate)
		assert.NotEmpty(t, receipts)
		header = block.Header()
		//assert.NotEqual(t, parent.Coinbase, header.Coinbase, "Header %v", i)
		assert.NotEqual(t, parent.Coinbase, common.Address{}, "Header %v", i)
		err = engine.VerifySeal(chain, header)
		assert.Empty(t, err)

		// Test consensus tx check during block processing
		//---
		if i == 2 {
			tmptxs := block.Transactions()
			tmpheader := *header

			assert.Equal(t, len(tmptxs), 1)

			_, _, err = engine.Finalize(
				chain, &tmpheader, blstate.Copy(), tmptxs, nil, receipts)
			assert.Empty(t, err)

			_, _, err = engine.Finalize(
				chain, &tmpheader, blstate.Copy(), append(tmptxs, tmptxs[len(tmptxs)-1]), nil, receipts)
			assert.Equal(t, eth_consensus.ErrInvalidConsensusTx, err)

			_, _, err = engine.Finalize(
				chain, &tmpheader, blstate.Copy(),
				append(tmptxs[:len(tmptxs)-1], tmptxs[len(tmptxs)-1].WithConsensusSender(common.Address{})),
				nil, receipts)
			assert.Equal(t, eth_consensus.ErrInvalidConsensusTx, err)
		}

		// Time tests
		//---
		tt := engine.calcTimeTarget(chain, parent)
		assert.True(t, tt.max_time >= now)
		assert.True(t, tt.max_time <= engine.now()+30)

		if i < 60 {
			assert.Equal(t, header.Time, parent.Time+30)

			assert.Equal(t, tt.min_time, header.Time)
			assert.Equal(t, tt.block_target, header.Time+30)
		} else if i < 61 {
			assert.Equal(t, header.Time, genesis.Time()+3570)
			assert.Equal(t, header.Time, parent.Time+1800)

			assert.Equal(t, tt.min_time, header.Time)
			assert.Equal(t, tt.block_target, parent.Time+60)
		} else if i < 62 {
			assert.Equal(t, header.Time, genesis.Time()+3600)
		}

		assert.True(t, parent.Time < tt.min_time, "Header %v", i)

		_, err = chain.WriteBlockWithState(block, receipts, blstate)
		assert.Empty(t, err)

		// Stake amount tests
		//---
		// TODO:

		//---

		parent = header
	}
}

func TestPoSDiffV1(t *testing.T) {
	t.Parallel()
	log.Root().SetHandler(log.StdoutHandler)

	type TC struct {
		parent  int64
		time    uint64
		min     uint64
		btarget uint64
		ptarget uint64
		result  uint64
	}

	tests := []TC{
		{
			parent:  100,
			time:    61,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  100,
		},
		{
			parent:  100,
			time:    31,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  1744,
		},
		{
			parent:  100,
			time:    31,
			min:     31,
			btarget: 51,
			ptarget: 71,
			result:  1744,
		},
		{
			parent:  100,
			time:    31,
			min:     61,
			btarget: 31,
			ptarget: 31,
			result:  1744,
		},
		{
			parent:  100,
			time:    31,
			min:     31,
			btarget: 31,
			ptarget: 31,
			result:  100,
		},
		{
			parent:  1744,
			time:    91,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  403,
		},
		{
			parent:  1744,
			time:    121,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  93,
		},
		{
			parent:  1744,
			time:    200,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  4,
		},
		{
			parent:  1744,
			time:    181,
			min:     31,
			btarget: 61,
			ptarget: 61,
			result:  4,
		},
	}

	for i, tc := range tests {
		parent := &types.Header{
			Difficulty: big.NewInt(tc.parent),
		}
		tt := &timeTarget{
			min_time:      tc.min,
			block_target:  tc.btarget,
			period_target: tc.ptarget,
		}

		res := calcPoSDifficultyV1(nil, tc.time, parent, tt)
		assert.Equal(t, tc.result, res.Uint64(), "TC %v", i)
	}
}
