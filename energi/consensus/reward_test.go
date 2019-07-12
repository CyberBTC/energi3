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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"

	"github.com/stretchr/testify/assert"
)

func TestBlockRewards(t *testing.T) {
	t.Parallel()
	log.Root().SetHandler(log.StdoutHandler)
	var (
		testdb = ethdb.NewMemDatabase()
		gspec  = &core.Genesis{
			Config: params.EnergiTestnetChainConfig,
			Xfers:  core.DeployEnergiGovernance(params.EnergiTestnetChainConfig),
		}
		parent = gspec.MustCommit(testdb)

		engine = New(new(params.EnergiConfig), testdb)
	)

	chain, _ := core.NewBlockChain(testdb, nil, params.EnergiTestnetChainConfig, ethash.NewFaker(), vm.Config{}, nil)
	defer chain.Stop()

	statedb, _ := state.New(parent.Root(), state.NewDatabase(testdb))

	header := &types.Header{
		Root:       statedb.IntermediateRoot(chain.Config().IsEIP158(parent.Number())),
		ParentHash: parent.Hash(),
		Coinbase:   parent.Coinbase(),
		Difficulty: engine.CalcDifficulty(chain, parent.Time()+10, &types.Header{
			Number:     parent.Number(),
			Time:       parent.Time(),
			Difficulty: parent.Difficulty(),
			UncleHash:  parent.UncleHash(),
		}),
		GasLimit: parent.GasLimit(),
		Number:   new(big.Int).Add(parent.Number(), common.Big1),
		Time:     parent.Time(),
	}

	err := engine.processConsensusGasLimits(chain, header, statedb)
	assert.Empty(t, err)

	for i := 0; i < 5; i++ {
		// TODO: check balance changes
		err = engine.processBlockRewards(chain, header, statedb)

		if err != nil {
			panic(err)
		}
	}

}
