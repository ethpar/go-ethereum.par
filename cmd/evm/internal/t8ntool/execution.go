// Copyright 2020 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package t8ntool

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/consensus/misc/eip4844"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"golang.org/x/crypto/sha3"
)

type Prestate struct {
	Env stEnv              `json:"env"`
	Pre types.GenesisAlloc `json:"pre"`
}

// ExecutionResult contains the execution status after running a state test, any
// error that might have occurred and a dump of the final state if requested.
type ExecutionResult struct {
	StateRoot             common.Hash                 `json:"stateRoot"`
	TxRoot                common.Hash                 `json:"txRoot"`
	ReceiptRoot           common.Hash                 `json:"receiptsRoot"`
	LogsHash              common.Hash                 `json:"logsHash"`
	Bloom                 types.Bloom                 `json:"logsBloom"        gencodec:"required"`
	Receipts              types.Receipts              `json:"receipts"`
	Rejected              []*rejectedTx               `json:"rejected,omitempty"`
	Difficulty            *math.HexOrDecimal256       `json:"currentDifficulty" gencodec:"required"`
	GasUsed               math.HexOrDecimal64         `json:"gasUsed"`
	BaseFee               *math.HexOrDecimal256       `json:"currentBaseFee,omitempty"`
	WithdrawalsRoot       *common.Hash                `json:"withdrawalsRoot,omitempty"`
	CurrentExcessBlobGas  *math.HexOrDecimal64        `json:"currentExcessBlobGas,omitempty"`
	CurrentBlobGasUsed    *math.HexOrDecimal64        `json:"blobGasUsed,omitempty"`
	RequestsHash          *common.Hash                `json:"requestsRoot,omitempty"`
	DepositRequests       types.Deposits              `json:"depositRequests,omitempty"`
	WithdrawalRequests    types.WithdrawalRequests    `json:"withdrawalRequests,omitempty"`
	ConsolidationRequests types.ConsolidationRequests `json:"consolidationRequests,omitempty"`
}

type ommer struct {
	Delta   uint64         `json:"delta"`
	Address common.Address `json:"address"`
}

//go:generate go run github.com/fjl/gencodec -type stEnv -field-override stEnvMarshaling -out gen_stenv.go
type stEnv struct {
	Coinbase              common.Address                      `json:"currentCoinbase"   gencodec:"required"`
	Difficulty            *big.Int                            `json:"currentDifficulty"`
	Random                *big.Int                            `json:"currentRandom"`
	ParentDifficulty      *big.Int                            `json:"parentDifficulty"`
	ParentBaseFee         *big.Int                            `json:"parentBaseFee,omitempty"`
	ParentGasUsed         uint64                              `json:"parentGasUsed,omitempty"`
	ParentGasLimit        uint64                              `json:"parentGasLimit,omitempty"`
	GasLimit              uint64                              `json:"currentGasLimit"   gencodec:"required"`
	Number                uint64                              `json:"currentNumber"     gencodec:"required"`
	Timestamp             uint64                              `json:"currentTimestamp"  gencodec:"required"`
	ParentTimestamp       uint64                              `json:"parentTimestamp,omitempty"`
	BlockHashes           map[math.HexOrDecimal64]common.Hash `json:"blockHashes,omitempty"`
	Ommers                []ommer                             `json:"ommers,omitempty"`
	Withdrawals           []*types.Withdrawal                 `json:"withdrawals,omitempty"`
	BaseFee               *big.Int                            `json:"currentBaseFee,omitempty"`
	ParentUncleHash       common.Hash                         `json:"parentUncleHash"`
	ExcessBlobGas         *uint64                             `json:"currentExcessBlobGas,omitempty"`
	ParentExcessBlobGas   *uint64                             `json:"parentExcessBlobGas,omitempty"`
	ParentBlobGasUsed     *uint64                             `json:"parentBlobGasUsed,omitempty"`
	ParentBeaconBlockRoot *common.Hash                        `json:"parentBeaconBlockRoot"`
}

type stEnvMarshaling struct {
	Coinbase            common.UnprefixedAddress
	Difficulty          *math.HexOrDecimal256
	Random              *math.HexOrDecimal256
	ParentDifficulty    *math.HexOrDecimal256
	ParentBaseFee       *math.HexOrDecimal256
	ParentGasUsed       math.HexOrDecimal64
	ParentGasLimit      math.HexOrDecimal64
	GasLimit            math.HexOrDecimal64
	Number              math.HexOrDecimal64
	Timestamp           math.HexOrDecimal64
	ParentTimestamp     math.HexOrDecimal64
	BaseFee             *math.HexOrDecimal256
	ExcessBlobGas       *math.HexOrDecimal64
	ParentExcessBlobGas *math.HexOrDecimal64
	ParentBlobGasUsed   *math.HexOrDecimal64
}

type rejectedTx struct {
	Index int    `json:"index"`
	Err   string `json:"error"`
}

// Apply applies a set of transactions to a pre-state
func (pre *Prestate) Apply(vmConfig vm.Config, chainConfig *params.ChainConfig,
	txIt txIterator, miningReward int64,
	getTracerFn func(txIndex int, txHash common.Hash) (*tracers.Tracer, io.WriteCloser, error)) (*state.StateDB, *ExecutionResult, []byte, error) {
	// Capture errors for BLOCKHASH operation, if we haven't been supplied the
	// required blockhashes
	var hashError error
	getHash := func(num uint64) common.Hash {
		if pre.Env.BlockHashes == nil {
			hashError = fmt.Errorf("getHash(%d) invoked, no blockhashes provided", num)
			return common.Hash{}
		}
		h, ok := pre.Env.BlockHashes[math.HexOrDecimal64(num)]
		if !ok {
			hashError = fmt.Errorf("getHash(%d) invoked, blockhash for that block not provided", num)
		}
		return h
	}
	var (
		statedb     = MakePreState(rawdb.NewMemoryDatabase(), pre.Pre)
		signer      = types.MakeSigner(chainConfig, new(big.Int).SetUint64(pre.Env.Number), pre.Env.Timestamp)
		gaspool     = new(core.GasPool)
		blockHash   = common.Hash{0x13, 0x37}
		rejectedTxs []*rejectedTx
		includedTxs types.Transactions
		gasUsed     = uint64(0)
		blobGasUsed = uint64(0)
		receipts    = make(types.Receipts, 0)
		txIndex     = 0
	)
	gaspool.AddGas(pre.Env.GasLimit)
	vmContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		Coinbase:    pre.Env.Coinbase,
		BlockNumber: new(big.Int).SetUint64(pre.Env.Number),
		Time:        pre.Env.Timestamp,
		Difficulty:  pre.Env.Difficulty,
		GasLimit:    pre.Env.GasLimit,
		GetHash:     getHash,
	}
	// If currentBaseFee is defined, add it to the vmContext.
	if pre.Env.BaseFee != nil {
		vmContext.BaseFee = new(big.Int).Set(pre.Env.BaseFee)
	}
	// If random is defined, add it to the vmContext.
	if pre.Env.Random != nil {
		rnd := common.BigToHash(pre.Env.Random)
		vmContext.Random = &rnd
	}
	// Calculate the BlobBaseFee
	var excessBlobGas uint64
	if pre.Env.ExcessBlobGas != nil {
		excessBlobGas = *pre.Env.ExcessBlobGas
		vmContext.BlobBaseFee = eip4844.CalcBlobFee(excessBlobGas)
	} else {
		// If it is not explicitly defined, but we have the parent values, we try
		// to calculate it ourselves.
		parentExcessBlobGas := pre.Env.ParentExcessBlobGas
		parentBlobGasUsed := pre.Env.ParentBlobGasUsed
		if parentExcessBlobGas != nil && parentBlobGasUsed != nil {
			excessBlobGas = eip4844.CalcExcessBlobGas(*parentExcessBlobGas, *parentBlobGasUsed)
			vmContext.BlobBaseFee = eip4844.CalcBlobFee(excessBlobGas)
		}
	}
	// If DAO is supported/enabled, we need to handle it here. In geth 'proper', it's
	// done in StateProcessor.Process(block, ...), right before transactions are applied.
	if chainConfig.DAOForkSupport &&
		chainConfig.DAOForkBlock != nil &&
		chainConfig.DAOForkBlock.Cmp(new(big.Int).SetUint64(pre.Env.Number)) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}
	if beaconRoot := pre.Env.ParentBeaconBlockRoot; beaconRoot != nil {
		evm := vm.NewEVM(vmContext, vm.TxContext{}, statedb, chainConfig, vmConfig)
		core.ProcessBeaconBlockRoot(*beaconRoot, evm, statedb)
	}
	if pre.Env.BlockHashes != nil && chainConfig.IsPrague(new(big.Int).SetUint64(pre.Env.Number), pre.Env.Timestamp) {
		var (
			prevNumber = pre.Env.Number - 1
			prevHash   = pre.Env.BlockHashes[math.HexOrDecimal64(prevNumber)]
			evm        = vm.NewEVM(vmContext, vm.TxContext{}, statedb, chainConfig, vmConfig)
		)
		core.ProcessParentBlockHash(prevHash, evm, statedb)
	}
	for i := 0; txIt.Next(); i++ {
		tx, err := txIt.Tx()
		if err != nil {
			log.Warn("rejected tx", "index", i, "error", err)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
			continue
		}
		if tx.Type() == types.BlobTxType && vmContext.BlobBaseFee == nil {
			errMsg := "blob tx used but field env.ExcessBlobGas missing"
			log.Warn("rejected tx", "index", i, "hash", tx.Hash(), "error", errMsg)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, errMsg})
			continue
		}
		msg, err := core.TransactionToMessage(tx, signer, pre.Env.BaseFee)
		if err != nil {
			log.Warn("rejected tx", "index", i, "hash", tx.Hash(), "error", err)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
			continue
		}
		txBlobGas := uint64(0)
		if tx.Type() == types.BlobTxType {
			txBlobGas = uint64(params.BlobTxBlobGasPerBlob * len(tx.BlobHashes()))
			if used, max := blobGasUsed+txBlobGas, uint64(params.MaxBlobGasPerBlock); used > max {
				err := fmt.Errorf("blob gas (%d) would exceed maximum allowance %d", used, max)
				log.Warn("rejected tx", "index", i, "err", err)
				rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
				continue
			}
		}
		tracer, traceOutput, err := getTracerFn(txIndex, tx.Hash())
		if err != nil {
			return nil, nil, nil, err
		}
		if tracer != nil {
			vmConfig.Tracer = tracer.Hooks
		}
		statedb.SetTxContext(tx.Hash(), txIndex)

		var (
			txContext = core.NewEVMTxContext(msg)
			snapshot  = statedb.Snapshot()
			prevGas   = gaspool.Gas()
		)
		evm := vm.NewEVM(vmContext, txContext, statedb, chainConfig, vmConfig)

		if tracer != nil && tracer.OnTxStart != nil {
			tracer.OnTxStart(evm.GetVMContext(), tx, msg.From)
		}
		// (ret []byte, usedGas uint64, failed bool, err error)
		msgResult, err := core.ApplyMessage(evm, msg, gaspool)
		if err != nil {
			statedb.RevertToSnapshot(snapshot)
			log.Info("rejected tx", "index", i, "hash", tx.Hash(), "from", msg.From, "error", err)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
			gaspool.SetGas(prevGas)
			if tracer != nil {
				if tracer.OnTxEnd != nil {
					tracer.OnTxEnd(nil, err)
				}
				if err := writeTraceResult(tracer, traceOutput); err != nil {
					log.Warn("Error writing tracer output", "err", err)
				}
			}
			continue
		}
		includedTxs = append(includedTxs, tx)
		if hashError != nil {
			return nil, nil, nil, NewError(ErrorMissingBlockhash, hashError)
		}
		blobGasUsed += txBlobGas
		gasUsed += msgResult.UsedGas

		// Receipt:
		{
			var root []byte
			if chainConfig.IsByzantium(vmContext.BlockNumber) {
				statedb.Finalise(true)
			} else {
				root = statedb.IntermediateRoot(chainConfig.IsEIP158(vmContext.BlockNumber)).Bytes()
			}

			// Create a new receipt for the transaction, storing the intermediate root and
			// gas used by the tx.
			receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: gasUsed}
			if msgResult.Failed() {
				receipt.Status = types.ReceiptStatusFailed
			} else {
				receipt.Status = types.ReceiptStatusSuccessful
			}
			receipt.TxHash = tx.Hash()
			receipt.GasUsed = msgResult.UsedGas

			// If the transaction created a contract, store the creation address in the receipt.
			if msg.To == nil {
				receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
			}

			// Set the receipt logs and create the bloom filter.
			receipt.Logs = statedb.GetLogs(tx.Hash(), vmContext.BlockNumber.Uint64(), blockHash)
			receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
			// These three are non-consensus fields:
			//receipt.BlockHash
			//receipt.BlockNumber
			receipt.TransactionIndex = uint(txIndex)
			receipts = append(receipts, receipt)
			if tracer != nil {
				if tracer.Hooks.OnTxEnd != nil {
					tracer.Hooks.OnTxEnd(receipt, nil)
				}
				if err = writeTraceResult(tracer, traceOutput); err != nil {
					log.Warn("Error writing tracer output", "err", err)
				}
			}
		}

		txIndex++
	}
	statedb.IntermediateRoot(chainConfig.IsEIP158(vmContext.BlockNumber))
	// Add mining reward? (-1 means rewards are disabled)
	if miningReward >= 0 {
		// Add mining reward. The mining reward may be `0`, which only makes a difference in the cases
		// where
		// - the coinbase self-destructed, or
		// - there are only 'bad' transactions, which aren't executed. In those cases,
		//   the coinbase gets no txfee, so isn't created, and thus needs to be touched
		var (
			blockReward = big.NewInt(miningReward)
			minerReward = new(big.Int).Set(blockReward)
			perOmmer    = new(big.Int).Rsh(blockReward, 5)
		)
		for _, ommer := range pre.Env.Ommers {
			// Add 1/32th for each ommer included
			minerReward.Add(minerReward, perOmmer)
			// Add (8-delta)/8
			reward := big.NewInt(8)
			reward.Sub(reward, new(big.Int).SetUint64(ommer.Delta))
			reward.Mul(reward, blockReward)
			reward.Rsh(reward, 3)
			statedb.AddBalance(ommer.Address, uint256.MustFromBig(reward), tracing.BalanceIncreaseRewardMineUncle)
		}
		statedb.AddBalance(pre.Env.Coinbase, uint256.MustFromBig(minerReward), tracing.BalanceIncreaseRewardMineBlock)
	}
	// Apply withdrawals
	for _, w := range pre.Env.Withdrawals {
		// Amount is in gwei, turn into wei
		amount := new(big.Int).Mul(new(big.Int).SetUint64(w.Amount), big.NewInt(params.GWei))
		statedb.AddBalance(w.Address, uint256.MustFromBig(amount), tracing.BalanceIncreaseWithdrawal)
	}
	// Retrieve deposit and withdrawal requests
	var (
		depositRequests       types.Deposits
		withdrawalRequests    types.WithdrawalRequests
		consolidationRequests types.ConsolidationRequests
		requestsHash          *common.Hash
	)
	if chainConfig.IsPrague(vmContext.BlockNumber, vmContext.Time) {
		// Parse deposit requests from the logs
		var allLogs []*types.Log
		for _, receipt := range receipts {
			allLogs = append(allLogs, receipt.Logs...)
		}
		requests, err := core.ParseDepositLogs(allLogs, chainConfig)
		if err != nil {
			return nil, nil, nil, NewError(ErrorEVM, fmt.Errorf("could not parse requests logs: %v", err))
		}
		// Process the withdrawal requests contract execution
		vmenv := vm.NewEVM(vmContext, vm.TxContext{}, statedb, chainConfig, vmConfig)
		wxs := core.ProcessDequeueWithdrawalRequests(vmenv, statedb)
		requests = append(requests, wxs...)
		// Process the consolidation requests contract execution
		cxs := core.ProcessDequeueConsolidationRequests(vmenv, statedb)
		requests = append(requests, cxs...)
		// Calculate the requests root
		h := types.DeriveSha(requests, trie.NewStackTrie(nil))
		requestsHash = &h

		// Break out individual request types.
		depositRequests = make(types.Deposits, 0)
		withdrawalRequests = make(types.WithdrawalRequests, 0)
		consolidationRequests = make(types.ConsolidationRequests, 0)
		for _, req := range requests {
			switch v := req.Inner().(type) {
			case *types.Deposit:
				depositRequests = append(depositRequests, v)
			case *types.WithdrawalRequest:
				withdrawalRequests = append(withdrawalRequests, v)
			case *types.ConsolidationRequest:
				consolidationRequests = append(consolidationRequests, v)
			}
		}
	}
	// Commit block
	root, err := statedb.Commit(vmContext.BlockNumber.Uint64(), chainConfig.IsEIP158(vmContext.BlockNumber))
	if err != nil {
		return nil, nil, nil, NewError(ErrorEVM, fmt.Errorf("could not commit state: %v", err))
	}
	execRs := &ExecutionResult{
		StateRoot:             root,
		TxRoot:                types.DeriveSha(includedTxs, trie.NewStackTrie(nil)),
		ReceiptRoot:           types.DeriveSha(receipts, trie.NewStackTrie(nil)),
		Bloom:                 types.CreateBloom(receipts),
		LogsHash:              rlpHash(statedb.Logs()),
		Receipts:              receipts,
		Rejected:              rejectedTxs,
		Difficulty:            (*math.HexOrDecimal256)(vmContext.Difficulty),
		GasUsed:               (math.HexOrDecimal64)(gasUsed),
		BaseFee:               (*math.HexOrDecimal256)(vmContext.BaseFee),
		RequestsHash:          requestsHash,
		DepositRequests:       depositRequests,
		WithdrawalRequests:    withdrawalRequests,
		ConsolidationRequests: consolidationRequests,
	}
	if pre.Env.Withdrawals != nil {
		h := types.DeriveSha(types.Withdrawals(pre.Env.Withdrawals), trie.NewStackTrie(nil))
		execRs.WithdrawalsRoot = &h
	}
	if vmContext.BlobBaseFee != nil {
		execRs.CurrentExcessBlobGas = (*math.HexOrDecimal64)(&excessBlobGas)
		execRs.CurrentBlobGasUsed = (*math.HexOrDecimal64)(&blobGasUsed)
	}
	// Re-create statedb instance with new root upon the updated database
	// for accessing latest states.
	statedb, err = state.New(root, statedb.Database(), nil)
	if err != nil {
		return nil, nil, nil, NewError(ErrorEVM, fmt.Errorf("could not reopen state: %v", err))
	}
	body, _ := rlp.EncodeToBytes(includedTxs)
	return statedb, execRs, body, nil
}

func MakePreState(db ethdb.Database, accounts types.GenesisAlloc) *state.StateDB {
	sdb := state.NewDatabaseWithConfig(db, &triedb.Config{Preimages: true})
	statedb, _ := state.New(types.EmptyRootHash, sdb, nil)
	for addr, a := range accounts {
		statedb.SetCode(addr, a.Code)
		statedb.SetNonce(addr, a.Nonce)
		statedb.SetBalance(addr, uint256.MustFromBig(a.Balance), tracing.BalanceIncreaseGenesisBalance)
		for k, v := range a.Storage {
			statedb.SetState(addr, k, v)
		}
	}
	// Commit and re-open to start with a clean state.
	root, _ := statedb.Commit(0, false)
	statedb, _ = state.New(root, sdb, nil)
	return statedb
}

func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

// calcDifficulty is based on ethash.CalcDifficulty. This method is used in case
// the caller does not provide an explicit difficulty, but instead provides only
// parent timestamp + difficulty.
// Note: this method only works for ethash engine.
func calcDifficulty(config *params.ChainConfig, number, currentTime, parentTime uint64,
	parentDifficulty *big.Int, parentUncleHash common.Hash) *big.Int {
	uncleHash := parentUncleHash
	if uncleHash == (common.Hash{}) {
		uncleHash = types.EmptyUncleHash
	}
	parent := &types.Header{
		ParentHash: common.Hash{},
		UncleHash:  uncleHash,
		Difficulty: parentDifficulty,
		Number:     new(big.Int).SetUint64(number - 1),
		Time:       parentTime,
	}
	return ethash.CalcDifficulty(config, currentTime, parent)
}

func writeTraceResult(tracer *tracers.Tracer, f io.WriteCloser) error {
	defer f.Close()
	result, err := tracer.GetResult()
	if err != nil || result == nil {
		return err
	}
	err = json.NewEncoder(f).Encode(result)
	if err != nil {
		return err
	}
	return nil
}