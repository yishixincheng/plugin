// Copyright Fuzamei Corp. 2018 All Rights Reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"math/big"
	"sync/atomic"

	log "github.com/33cn/chain33/common/log/log15"

	"github.com/33cn/chain33/types"
	"github.com/33cn/plugin/plugin/dapp/evm/executor/vm/common"
	"github.com/33cn/plugin/plugin/dapp/evm/executor/vm/gas"
	"github.com/33cn/plugin/plugin/dapp/evm/executor/vm/model"
	"github.com/33cn/plugin/plugin/dapp/evm/executor/vm/params"
	"github.com/33cn/plugin/plugin/dapp/evm/executor/vm/state"
	evmtypes "github.com/33cn/plugin/plugin/dapp/evm/types"
)

type (
	// CanTransferFunc 检查制定账户是否有足够的金额进行转账
	CanTransferFunc func(state.EVMStateDB, common.Address, uint64) bool

	// TransferFunc 执行转账逻辑
	TransferFunc func(state.EVMStateDB, common.Address, common.Address, uint64) bool

	// GetHashFunc 获取制定高度区块的哈希
	// 给 BLOCKHASH 指令使用
	GetHashFunc func(uint64) common.Hash
)

// 依据合约地址判断是否为预编译合约，如果不是，则全部通过解释器解释执行
func run(evm *EVM, contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {
	if contract.CodeAddr != nil {
		p, sp, _ := evm.precompile(*contract.CodeAddr)
		if p != nil {
			ret, contract.Gas, err = RunPrecompiledContract(p, input, contract.Gas)
		} else if sp != nil {
			ret, contract.Gas, err = RunStateFulPrecompiledContract(evm, contract, sp, input, contract.Gas)
		}
	}
	// 在此处打印下自定义合约的错误信息
	ret, err = evm.Interpreter.Run(contract, input, readOnly)
	if err != nil {
		log.Error("error occurs while run evm contract", "error info", err, "input:", common.Bytes2Hex(input), "size:", len(input))
	}
	return ret, err
}

// Context EVM操作辅助上下文
// 外部在构造EVM实例时传入，EVM实例构造完成后不允许修改
type Context struct {

	// CanTransfer 是否可转账
	CanTransfer CanTransferFunc
	// Transfer 转账
	Transfer TransferFunc
	// GetHash 获取区块哈希
	GetHash GetHashFunc

	// Origin 指令返回数据， 合约调用者地址
	Origin common.Address
	// GasPrice 指令返回数据
	GasPrice uint32

	// Coinbase 指令， 区块打包者地址
	Coinbase *common.Address
	// GasLimit 指令，当前交易的GasLimit
	GasLimit uint64

	// TxHash
	TxHash []byte

	// BlockNumber NUMBER 指令，当前区块高度
	BlockNumber *big.Int
	// Time 指令， 当前区块打包时间
	Time *big.Int
	// Difficulty 指令，当前区块难度
	Difficulty *big.Int
}

// EVM 结构对象及其提供的操作方法，用于进行满足以太坊EVM黄皮书规范定义的智能合约代码的创建和执行
// 合约执行过程中会修改外部状态数据（数据操作通过外部注入）
// 在合约代码执行过程中发生的任何错误，都将会导致对状态数据的修改被回滚，并且依然消耗掉剩余的Gas
// 此对象为每个交易创建一个实例，其操作非线程安全
type EVM struct {
	// Context 链相关的一些辅助属性和操作方法
	Context
	// EVMStateDB 状态数据操作入口
	StateDB state.EVMStateDB
	// 当前调用深度
	depth int

	// VMConfig 虚拟机配置属性信息
	VMConfig Config

	// Interpreter EVM指令解释器，生命周期同EVM
	Interpreter *Interpreter

	// EVM执行流程结束标志
	// 不允许手工设置
	abort int32

	// CallGasTemp 此属性用于临时存储计算出来的Gas消耗值
	// 在指令执行时，会调用指令的gasCost方法，计算出此指令需要消耗的Gas，并存放在此临时属性中
	// 然后在执行opCall时，从此属性获取消耗的Gas值
	callGasTemp uint64

	// 支持的最长合约代码大小
	maxCodeSize int

	// chain33配置
	cfg        *types.Chain33Config
	isEthTx    bool
	evmChainID int32
}

// NewEVM 创建一个新的EVM实例对象
// 在同一个节点中，一个EVM实例对象只服务于一个交易执行的生命周期
func NewEVM(ctx Context, statedb state.EVMStateDB, vmConfig Config, cfg *types.Chain33Config) *EVM {
	evm := &EVM{
		Context:     ctx,
		StateDB:     statedb,
		VMConfig:    vmConfig,
		maxCodeSize: params.MaxCodeSize,
		cfg:         cfg,
	}
	var subCof struct {
		EvmChainID int32 `json:"evmChainID,omitempty"`
	}
	types.MustDecode(cfg.GetSubConfig().Crypto["secp256k1eth"], &subCof)
	evm.evmChainID = subCof.EvmChainID
	evm.Interpreter = NewInterpreter(evm, vmConfig)
	return evm
}

// GasTable 返回不同操作消耗的Gas定价表
// 接收区块高度作为参数，方便以后在这里作分叉处理
func (evm *EVM) GasTable(num *big.Int) gas.Table {
	return gas.TableHomestead
}

// Cancel 调用此操作会在任意时刻取消此EVM的解释运行逻辑，支持重复调用
func (evm *EVM) Cancel() {
	atomic.StoreInt32(&evm.abort, 1)
}

// SetMaxCodeSize 设置合约代码的最大支持长度
func (evm *EVM) SetMaxCodeSize(maxCodeSize int) {
	if maxCodeSize < 1 || maxCodeSize > params.MaxCodeSize {
		return
	}

	evm.maxCodeSize = maxCodeSize
}

//SetEthTxFlag 设置eth tx 标志
func (evm *EVM) SetEthTxFlag(ok bool) {
	evm.isEthTx = ok
}

//CheckIsEthTx  查看是否是eth tx
func (evm *EVM) CheckIsEthTx() bool {
	return evm.isEthTx
}

// 封装合约的各种调用逻辑中通用的预检查逻辑
func (evm *EVM) preCheck(caller ContractRef, value uint64) (pass bool, err error) {
	// 检查调用深度是否合法
	if evm.VMConfig.NoRecursion && evm.depth > 0 {
		return false, nil
	}

	// 允许递归，但深度不合法
	if evm.depth > int(params.CallCreateDepth) {
		return false, model.ErrDepth
	}

	// 如有转账，检查余额是否充足
	if value > 0 {
		if !evm.Context.CanTransfer(evm.StateDB, caller.Address(), value) {
			return false, model.ErrInsufficientBalance
		}
	}

	// 检查通过情况下，后面三个返回参数无意义
	return true, nil
}

// Call 此方法提供合约外部调用入口
// 根据合约地址调用已经存在的合约，input为合约调用参数
// 合约调用逻辑支持在合约调用的同时进行向合约转账的操作
func (evm *EVM) Call(caller ContractRef, addr common.Address, input []byte, gas uint64, value uint64) (ret []byte, snapshot int, leftOverGas uint64, err error) {
	log.Info("Call", "caller:", caller.Address().String(), "addr:", addr.String(), "gas:", gas, "isEtx:", evm.CheckIsEthTx(), "value:", value, "inputsize:", len(input), "inputData:", common.Bytes2Hex(input))
	pass, err := evm.preCheck(caller, value)
	if !pass {
		log.Error("Call", "preCheck:", err)
		return nil, -1, gas, err
	}

	p, sp, isPrecompile := evm.precompile(addr)
	if !evm.StateDB.Exist(addr.String()) {
		// 合约地址在自定义合约和预编译合约中都不存在时，可能为外部账户
		if !isPrecompile && value == 0 {
			// 只有一种情况会走到这里来，就是合约账户向外部账户转账的情况
			//if len(input) > 0 || value == 0 {
			// 其它情况要求地址必须存在，所以需要报错
			if EVMDebugOn == evm.VMConfig.Debug && evm.depth == 0 {
				evm.VMConfig.Tracer.CaptureStart(caller.Address(), addr, false, input, gas, value)
				evm.VMConfig.Tracer.CaptureEnd(ret, 0, 0, nil)
			}
			return nil, -1, gas, nil
			//return nil, -1, gas, model.ErrAddrNotExists
			//}
		} else {
			// 否则，为预编译合约，创建一个新的账号
			// 此分支先屏蔽，不需要为预编译合约创建账号也可以调用合约逻辑，因为预编译合约只有逻辑没有存储状态，可以不对应具体的账号存储
			//evm.StateDB.CreateAccount(addr, caller.Address())

		}
	}

	// 如果是已经销毁状态的合约是不允许调用的
	if evm.StateDB.HasSuicided(addr.String()) {
		return nil, -1, gas, model.ErrDestruct
	}
	// 打快照，开始处理逻辑
	snapshot = evm.StateDB.Snapshot()
	to := AccountRef(addr)
	// 向合约地址转账
	evm.Transfer(evm.StateDB, caller.Address(), to.Address(), value)
	log.Info("evm call", "caller address", caller.Address().String(), "contract address", to.Address().String(), "value", value)
	// 从ForkV20EVMState开始，状态数据存储发生变更，需要做数据迁移
	cfg := evm.StateDB.GetConfig()
	if cfg.IsDappFork(evm.BlockNumber.Int64(), "evm", evmtypes.ForkEVMState) {
		evm.StateDB.TransferStateData(addr.String())
	}

	if isPrecompile {
		if p != nil {
			ret, gas, err = RunPrecompiledContract(p, input, gas)
		} else {
			//执行自定义的预编译合约功能
			ret, gas, err = RunStateFulPrecompiledContract(evm, caller, sp, input, gas)
		}

	} else {
		// Initialise a new contract and set the code that is to be used by the EVM.
		// The contract is a scoped environment for this execution context only.
		code := evm.StateDB.GetCode(addr.String())
		if len(code) == 0 {
			ret, err = nil, nil // gas is unchanged
		} else {
			// 创建新的合约对象，包含双方地址以及合约代码，可用Gas信息
			var bigValue = new(big.Int).SetUint64(value)
			if evm.CheckIsEthTx() && value != 0 {
				//把value 的精度从1e8 再次回到1e18
				bigValue = evm.conversion2EthPrecision(bigValue)
			}
			contract := NewContract(caller, AccountRef(addr), bigValue, gas)
			contract.SetCallCode(&addr, evm.StateDB.GetCodeHash(addr.String()), evm.StateDB.GetCode(addr.String()))
			start := types.Now()

			// 调试模式下启用跟踪
			if EVMDebugOn == evm.VMConfig.Debug && evm.depth == 0 {
				evm.VMConfig.Tracer.CaptureStart(caller.Address(), addr, false, input, gas, value)
				defer func() {
					evm.VMConfig.Tracer.CaptureEnd(ret, gas-contract.Gas, types.Since(start), err)
				}()
			}
			ret, err = run(evm, contract, input, false)
			gas = contract.Gas
		}
	}
	// 当合约调用出错时，操作将会回滚（对数据的变更操作会被恢复），并且会消耗掉所有的gas
	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != model.ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, snapshot, gas, err
}

// CallCode 合约内部调用合约的入口
// 执行逻辑同Call方法，但是有以下几点不同：
// 在创建合约对象时，合约对象的上下文地址（合约对象的self属性）被设置为caller的地址
func (evm *EVM) CallCode(caller ContractRef, addr common.Address, input []byte, gas uint64, value uint64) (ret []byte, leftOverGas uint64, err error) {
	log.Info("CallCode", "caller:", caller.Address(), "addr:", addr, "input:", common.Bytes2Hex(input))
	pass, err := evm.preCheck(caller, value)
	if !pass {
		return nil, gas, err
	}

	// 如果是已经销毁状态的合约是不允许调用的
	if evm.StateDB.HasSuicided(addr.String()) {
		return nil, gas, model.ErrDestruct
	}

	var (
		snapshot = evm.StateDB.Snapshot()
		to       = AccountRef(caller.Address())
	)

	// It is allowed to call precompiles, even via delegatecall
	if p, sp, isPrecompile := evm.precompile(addr); isPrecompile {
		if p != nil {
			ret, gas, err = RunPrecompiledContract(p, input, gas)
		} else {
			//执行自定义的与变异合约调用
			ret, gas, err = RunStateFulPrecompiledContract(evm, caller, sp, input, gas)
		}

	} else {
		var bigValue = new(big.Int).SetUint64(value)
		if evm.CheckIsEthTx() && value != 0 {
			//把value 的精度从1e8 再次回到1e18
			bigValue = evm.conversion2EthPrecision(new(big.Int).SetUint64(value))
		}

		// 创建合约对象时，讲调用者和被调用者地址均设置为外部账户地址
		contract := NewContract(caller, to, bigValue, gas)
		// 正常从合约地址加载合约代码
		contract.SetCallCode(&addr, evm.StateDB.GetCodeHash(addr.String()), evm.StateDB.GetCode(addr.String()))
		ret, err = run(evm, contract, input, false)
		gas = contract.Gas

	}

	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// DelegateCall 合约内部调用合约的入口
// 不支持向合约转账
// 和CallCode不同的是，它会把合约的外部调用地址设置成caller的caller
func (evm *EVM) DelegateCall(caller ContractRef, addr common.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {
	log.Info("DelegateCall", "caller:", caller.Address(), "addr:", addr, "input:", common.Bytes2Hex(input))
	pass, err := evm.preCheck(caller, 0)
	if !pass {
		return nil, gas, err
	}

	// 如果是已经销毁状态的合约是不允许调用的
	if evm.StateDB.HasSuicided(addr.String()) {
		return nil, gas, model.ErrDestruct
	}

	var (
		snapshot = evm.StateDB.Snapshot()
		to       = AccountRef(caller.Address())
	)

	if p, sp, isPrecompile := evm.precompile(addr); isPrecompile {
		if p != nil {
			ret, gas, err = RunPrecompiledContract(p, input, gas)
		} else {
			ret, gas, err = RunStateFulPrecompiledContract(evm, caller, sp, input, gas)
		}

	} else {
		// 同外部合约的创建和修改逻辑，在每次调用时，需要创建并初始化一个新的合约内存对象
		// 需要注意，这里不同的是，需要设置合约的委托调用模式（会进行一些属性设置）
		contract := NewContract(caller, to, big.NewInt(0), gas).AsDelegate()
		contract.SetCallCode(&addr, evm.StateDB.GetCodeHash(addr.String()), evm.StateDB.GetCode(addr.String()))
		// 其它逻辑同StaticCall
		ret, err = run(evm, contract, input, false)
		gas = contract.Gas

	}

	if err != nil {
		evm.StateDB.RevertToSnapshot(snapshot)
		if err != model.ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// StaticCall 合约内部调用合约的入口
// 不支持向合约转账
// 在合约逻辑中，可以指定其它的合约地址以及输入参数进行合约调用，但是，这种情况下禁止修改MemoryStateDB中的任何数据，否则执行会出错
func (evm *EVM) StaticCall(caller ContractRef, addr common.Address, input []byte, gas uint64) (ret []byte, leftOverGas uint64, err error) {

	addrecrecover := common.BytesToAddress(common.RightPadBytes([]byte{1}, 20))
	log.Info("StaticCall", "input", common.Bytes2Hex(input),
		"addr slice", common.Bytes2Hex(addr.Bytes()),
		"addrecrecover", addrecrecover.String(),
		"addrecrecoverslice", common.Bytes2Hex(addrecrecover.Bytes()), "caller", caller.Address(), "gas", gas)

	pass, err := evm.preCheck(caller, 0)
	if !pass {
		return nil, gas, err
	}

	isPrecompile := false
	var p PrecompiledContract
	var sp StatefulPrecompiledContract

	//precompiles := PrecompiledContractsByzantium
	if !evm.StateDB.Exist(addr.String()) {
		//预编译分叉处理： chain33中目前只存在拜占庭和最新的黄皮书v1版本（兼容伊斯坦布尔版本）

		// 是否是黄皮书v1分叉
		/*if evm.cfg.IsDappFork(evm.StateDB.GetBlockHeight(), "evm", evmtypes.ForkEVMYoloV1) {
			precompiles = PrecompiledContractsIstanbul
		}
		// 合约地址在自定义合约和预编译合约中都不存在时，可能为外部账户
		if precompiles[addr.ToHash160()] == nil {*/
		p, sp, isPrecompile = evm.precompile(addr)
		if !isPrecompile {
			// 只有一种情况会走到这里来，就是合约账户向外部账户转账的情况
			if len(input) > 0 {
				return nil, gas, model.ErrAddrNotExists
			}
		} else {
			//isPrecompile = true
			log.Info("StaticCall", "addr.Bytes()", common.Bytes2Hex(addr.Bytes()),
				"isPrecompile", isPrecompile)
		}
	}
	// 如果是已经销毁状态的合约是不允许调用的
	if evm.StateDB.HasSuicided(addr.String()) {
		return nil, gas, model.ErrDestruct
	}

	// 如果指令解释器没有设置成只读，需要在这里强制设置，并在本操作结束后恢复
	// 在解释器执行涉及到数据修改指令时，会检查此属性，从而控制不允许修改数据
	if !evm.Interpreter.readOnly {
		evm.Interpreter.readOnly = true
		defer func() { evm.Interpreter.readOnly = false }()
	}
	var (
		to       = AccountRef(addr)
		snapshot = evm.StateDB.Snapshot()
	)

	contract := NewContract(caller, to, big.NewInt(0), gas)
	if isPrecompile {
		if p != nil {
			ret, gas, err = RunPrecompiledContract(p, input, gas)
		} else {
			ret, gas, err = RunStateFulPrecompiledContract(evm, caller, sp, input, gas)
		}

	} else {
		contract.SetCallCode(&addr, evm.StateDB.GetCodeHash(addr.String()), evm.StateDB.GetCode(addr.String()))
		// 执行合约指令时如果出错，需要进行回滚，并且扣除剩余的Gas
		ret, err = run(evm, contract, input, false)
		gas = contract.Gas
	}
	// 同外部合约的创建和修改逻辑，在每次调用时，需要创建并初始化一个新的合约内存对象
	if err != nil {
		// 合约执行出错时进行回滚
		// 注意，虽然内部调用合约不允许变更数据，但是可以进行生成日志等其它操作，这种情况下也是需要回滚的
		evm.StateDB.RevertToSnapshot(snapshot)
		// 如果操作消耗了资源，即使失败，也扣除剩余的Gas
		if err != model.ErrExecutionReverted {
			gas = 0
		}
	}
	return ret, gas, err
}

// Create 此方法提供合约外部创建入口；
// 使用传入的部署代码创建新的合约；
// 目前chain33为了保证账户安全，不允许合约中涉及到外部账户的转账操作，
// 所以，本步骤不接收转账金额参数
func (evm *EVM) Create(caller ContractRef, contractAddr common.Address, code []byte, gas uint64, execName, alias string, value uint64) (ret []byte, snapshot int, leftOverGas uint64, err error) {
	pass, err := evm.preCheck(caller, value)
	if !pass {
		return nil, -1, gas, err
	}

	evm.Transfer(evm.StateDB, caller.Address(), contractAddr, value)

	// 创建新的合约对象，包含双方地址以及合约代码，可用Gas信息
	var bigValue = new(big.Int).SetUint64(value)
	if evm.CheckIsEthTx() {
		if evm.CheckIsEthTx() && value != 0 {
			//把value 的精度从1e8 再次回到1e18
			bigValue = evm.conversion2EthPrecision(new(big.Int).SetUint64(value))
			//ethUnit := big.NewInt(1e18)
			//bigValue = big.NewInt(1).Mul(big.NewInt(int64(value)), ethUnit.Div(ethUnit, big.NewInt(1).SetInt64(evm.cfg.GetCoinPrecision())))

		}
	}
	contract := NewContract(caller, AccountRef(contractAddr), bigValue, gas)
	contract.SetCallCode(&contractAddr, common.ToHash(code), code)

	// 创建一个新的账户对象（合约账户）
	snapshot = evm.StateDB.Snapshot()
	evm.StateDB.CreateAccount(contractAddr.String(), contract.CallerAddress.String(), execName, alias)

	if EVMDebugOn == evm.VMConfig.Debug && evm.depth == 0 {
		evm.VMConfig.Tracer.CaptureStart(caller.Address(), contractAddr, true, code, gas, 0)
	}
	start := types.Now()
	// 通过预编译指令和解释器执行合约
	ret, err = run(evm, contract, nil, false)
	// 检查部署后的合约代码大小是否超限
	maxCodeSizeExceeded := len(ret) > evm.maxCodeSize
	// 如果执行成功，计算存储合约代码需要花费的Gas
	if err == nil && !maxCodeSizeExceeded {
		createDataGas := uint64(len(ret)) * params.CreateDataGas
		if contract.UseGas(createDataGas) {
			evm.StateDB.SetCode(contractAddr.String(), ret)
		} else {
			// 如果Gas不足，返回这个错误，让外部程序处理
			err = model.ErrCodeStoreOutOfGas
		}
	}

	// 如果合约代码超大，或者出现除Gas不足外的其它错误情况
	// 则回滚本次合约创建操作
	if maxCodeSizeExceeded || (err != nil && err != model.ErrCodeStoreOutOfGas) {
		evm.StateDB.RevertToSnapshot(snapshot)

		// 如果之前步骤出错，且没有回滚过，则扣除Gas
		if err != model.ErrExecutionReverted {
			contract.UseGas(contract.Gas)
		}
	}

	// 如果前面的步骤都没有问题，单纯只是合约大小超大，则设置错误为合约代码超限，让外部程序处理
	if maxCodeSizeExceeded && err == nil {
		err = model.ErrMaxCodeSizeExceeded
	}

	if EVMDebugOn == evm.VMConfig.Debug && evm.depth == 0 {
		evm.VMConfig.Tracer.CaptureEnd(ret, gas-contract.Gas, types.Since(start), err)
	}

	return ret, snapshot, contract.Gas, err
}

func (evm *EVM) precompile(addr common.Address) (PrecompiledContract, StatefulPrecompiledContract, bool) {
	p, ok := PrecompiledContractsBerlin[addr.ToHash160()]
	if ok {
		return p, nil, ok
	}
	//增加了自定义的预编译合约判断
	cp, ok := CustomizePrecompiledContracts[addr.ToHash160()]
	return nil, cp, ok

}

func (evm *EVM) CheckPrecompile(addr common.Address) bool {
	_, _, ok := evm.precompile(addr)
	return ok
}

//conversion2EthPrecision 把底层精度转换为eth 精度
func (evm *EVM) conversion2EthPrecision(num *big.Int) *big.Int {
	ethUnit := big.NewInt(1e18)
	mulUnit := new(big.Int).Div(ethUnit, big.NewInt(1).SetInt64(evm.cfg.GetCoinPrecision()))
	return new(big.Int).Mul(num, mulUnit)
}

//ethPrecision2Chain33Standard 把eth 表示的精度值转换为底层精度值
func (evm *EVM) ethPrecision2Chain33Standard(num *big.Int) *big.Int {
	ethUnit := big.NewInt(1e18)
	return new(big.Int).Div(num, ethUnit.Div(ethUnit, big.NewInt(1).SetInt64(evm.cfg.GetCoinPrecision())))
}
