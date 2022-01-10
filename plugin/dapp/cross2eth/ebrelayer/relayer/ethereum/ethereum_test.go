package ethereum

import (
	"context"
	"flag"
	"fmt"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/contracts/contracts4eth/generated"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/33cn/chain33/client/mocks"
	dbm "github.com/33cn/chain33/common/db"
	_ "github.com/33cn/chain33/system"
	chain33Types "github.com/33cn/chain33/types"
	"github.com/33cn/chain33/util/testnode"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/contracts/test/setup"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/relayer/ethereum/ethinterface"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/relayer/ethereum/ethtxs"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/relayer/events"
	ebTypes "github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/types"
	relayerTypes "github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/types"
	"github.com/33cn/plugin/plugin/dapp/cross2eth/ebrelayer/utils"
	tml "github.com/BurntSushi/toml"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	configPath          = flag.String("f", "./../../relayer.toml", "configfile")
	ethPrivateKeyStr    = "0x3fa21584ae2e4fd74db9b58e2386f5481607dfa4d7ba0617aaa7858e5025dc1e"
	ethAccountAddr      = "0x92c8b16afd6d423652559c6e266cbe1c29bfd84f"
	chain33ReceiverAddr = "1BCGLhdcdthNutQowV2YShuuN9fJRRGLxu"
	passphrase          = "123456hzj"
	chainTestCfg        = chain33Types.NewChain33Config(chain33Types.GetDefaultCfgstring())

	//// 0x8AFDADFC88a1087c9A1D6c0F5Dd04634b87F303a
	//deployerPrivateKey = "8656d2bc732a8a816a461ba5e2d8aac7c7f85c26a813df30d5327210465eb230"
	//// 0x92C8b16aFD6d423652559C6E266cBE1c29Bfd84f
	//ethValidatorAddrKeyA = "3fa21584ae2e4fd74db9b58e2386f5481607dfa4d7ba0617aaa7858e5025dc1e"
	//ethValidatorAddrKeyB = "a5f3063552f4483cfc20ac4f40f45b798791379862219de9e915c64722c1d400"
	//ethValidatorAddrKeyC = "bbf5e65539e9af0eb0cfac30bad475111054b09c11d668fc0731d54ea777471e"
	//ethValidatorAddrKeyD = "c9fa31d7984edf81b8ef3b40c761f1847f6fcd5711ab2462da97dc458f1f896b"
)

func init() {
	fmt.Println("======================= init =======================")
	var tx chain33Types.Transaction
	var ret chain33Types.Reply
	ret.IsOk = true

	mockapi := &mocks.QueueProtocolAPI{}
	// 这里对需要mock的方法打桩,Close是必须的，其它方法根据需要
	mockapi.On("Close").Return()
	mockapi.On("AddPushSubscribe", mock.Anything).Return(&ret, nil)
	mockapi.On("CreateTransaction", mock.Anything).Return(&tx, nil)
	mockapi.On("SendTx", mock.Anything).Return(&ret, nil)
	mockapi.On("SendTransaction", mock.Anything).Return(&ret, nil)
	mockapi.On("GetConfig", mock.Anything).Return(chainTestCfg, nil)

	mock33 := testnode.New("", mockapi)
	defer mock33.Close()
	rpcCfg := mock33.GetCfg().RPC
	// 这里必须设置监听端口，默认的是无效值
	rpcCfg.JrpcBindAddr = "127.0.0.1:8801"
	mock33.GetRPC().Listen()
}

func Test_GetValidatorAddr(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	_, err = ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	_, _, err = NewAccount()
	require.Nil(t, err)

	privateKey, _, err := ethRelayer.GetAccount("123")
	require.Nil(t, err)
	assert.NotEqual(t, privateKey, ethPrivateKeyStr)

	privateKey, addr, err := ethRelayer.GetAccount(passphrase)
	require.Nil(t, err)
	assert.Equal(t, privateKey, ethPrivateKeyStr)
	assert.Equal(t, addr, ethAccountAddr)

	validators, err := ethRelayer.GetValidatorAddr()
	require.Nil(t, err)
	assert.Equal(t, validators.EthereumValidator, ethAccountAddr)
}

func Test_IsValidatorActive(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	addr, err := ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	fmt.Println(addr)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	is, err := ethRelayer.IsValidatorActive(para.InitValidators[0].String())
	assert.Equal(t, is, true)
	require.Nil(t, err)

	is, err = ethRelayer.IsValidatorActive("0x0C05bA5c230fDaA503b53702aF1962e08D0C60BF")
	assert.Equal(t, is, false)
	require.Nil(t, err)

	_, err = ethRelayer.IsValidatorActive("123")
	require.Error(t, err)
}

func Test_ShowAddr(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	_, err = ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.NoError(t, err)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	ethRelayer.prePareSubscribeEvent()

	addr, err := ethRelayer.ShowBridgeBankAddr()
	require.Nil(t, err)
	assert.Equal(t, addr, x2EthDeployInfo.BridgeBank.Address.String())

	addr, err = ethRelayer.ShowBridgeRegistryAddr()
	require.Nil(t, err)
	assert.Equal(t, addr, x2EthDeployInfo.BridgeRegistry.Address.String())

	addr, err = ethRelayer.ShowOperator()
	require.Nil(t, err)
	assert.Equal(t, addr, para.Operator.String())
}

func Test_SetBridgeRegistryAddr(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	addr, err := ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	fmt.Println(addr)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	_ = ethRelayer.setBridgeRegistryAddr(x2EthDeployInfo.BridgeRegistry.Address.String())
	registrAddrInDB, err := ethRelayer.getBridgeRegistryAddr()
	require.Nil(t, err)
	assert.Equal(t, registrAddrInDB, x2EthDeployInfo.BridgeRegistry.Address.String())
}

func Test_LockEth(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	addr, err := ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	fmt.Println(addr)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	ctx := context.Background()
	bridgeBankBalance, err := sim.BalanceAt(ctx, x2EthDeployInfo.BridgeBank.Address, nil)
	require.Nil(t, err)
	assert.Equal(t, bridgeBankBalance.Int64(), int64(0))

	userAuth, err := ethtxs.PrepareAuth4MultiEthereum(sim, para.DeployPrivateKey, para.Operator, ethRelayer.Addr2TxNonce)
	require.Nil(t, err)
	_, err = x2EthContracts.BridgeBank.ConfigplatformTokenSymbol(userAuth, "ETH")
	require.Nil(t, err)
	sim.Commit()

	userOneAuth, err := ethtxs.PrepareAuth4MultiEthereum(sim, para.ValidatorPriKey[0], para.InitValidators[0], ethRelayer.Addr2TxNonce)
	require.Nil(t, err)

	//lock 50 eth
	chain33Sender := []byte("14KEKbYtKKQm4wMthSK9J4La4nAiidGozt")
	ethAmount := big.NewInt(50)
	userOneAuth.Value = ethAmount
	_, err = x2EthContracts.BridgeBank.Lock(userOneAuth, chain33Sender, common.Address{}, ethAmount)
	require.Nil(t, err)
	sim.Commit()

	bridgeBankBalance, err = sim.BalanceAt(ctx, x2EthDeployInfo.BridgeBank.Address, nil)
	require.Nil(t, err)
	assert.Equal(t, bridgeBankBalance.Int64(), ethAmount.Int64())

	for i := 0; i < int(ethRelayer.maturityDegree+1); i++ {
		sim.Commit()
	}

	time.Sleep(time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	balance, err := ethRelayer.ShowLockStatics("")
	require.Nil(t, err)
	assert.Equal(t, balance, "50")

	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)
}

//CreateBridgeToken ...
func CreateBridgeToken(symbol string, client ethinterface.EthClientSpec, para *ethtxs.OperatorInfo, x2EthDeployInfo *ethtxs.X2EthDeployInfo, x2EthContracts *ethtxs.X2EthContracts, addr2TxNonce map[common.Address]*ethtxs.NonceMutex) (string, error) {
	//订阅事件
	eventName := "LogNewBridgeToken"
	bridgeBankABI := ethtxs.LoadABI(ethtxs.BridgeBankABI)
	logNewBridgeTokenSig := bridgeBankABI.Events[eventName].ID.Hex()
	query := ethereum.FilterQuery{
		Addresses: []common.Address{x2EthDeployInfo.BridgeBank.Address},
	}
	// We will check logs for new events
	logs := make(chan types.Log)
	// Filter by contract and event, write results to logs
	sub, err := client.SubscribeFilterLogs(context.Background(), query, logs)
	if nil != err {
		fmt.Println("CreateBrigeToken", "failed to SubscribeFilterLogs", err.Error())
		return "", err
	}

	//创建token
	auth, err := ethtxs.PrepareAuth4MultiEthereum(client, para.PrivateKey, para.Address, addr2TxNonce)
	if nil != err {
		return "", err
	}

	_, err = x2EthContracts.BridgeBank.BridgeBankTransactor.CreateNewBridgeToken(auth, symbol)
	if nil != err {
		return "", err
	}

	sim, isSim := client.(*ethinterface.SimExtend)
	if isSim {
		fmt.Println("Use the simulator")
		sim.Commit()
	}

	logEvent := &events.LogNewBridgeToken{}
	select {
	// Handle any errors
	case err := <-sub.Err():
		return "", err
	// vLog is raw event data
	case vLog := <-logs:
		// Check if the event is a 'LogLock' event
		if vLog.Topics[0].Hex() == logNewBridgeTokenSig {
			fmt.Println("CreateBrigeToken", "Witnessed new event", eventName, "Block number", vLog.BlockNumber)

			err = bridgeBankABI.UnpackIntoInterface(logEvent, eventName, vLog.Data)
			if nil != err {
				return "", err
			}
			if symbol != logEvent.Symbol {
				fmt.Println("CreateBrigeToken", "symbol", symbol, "logEvent.Symbol", logEvent.Symbol)
			}
			fmt.Println("CreateBrigeToken", "Witnessed new event", eventName, "Block number", vLog.BlockNumber, "token address", logEvent.Token.String())
			break
		}
	}
	return logEvent.Token.String(), nil
}

func Test_CreateBridgeToken(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	_, err = ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.NoError(t, err)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	balance, err := ethRelayer.GetBalance("", para.InitValidators[0].String())
	require.Nil(t, err)
	assert.Equal(t, balance, "10000000000000000")

	tokenAddrbty, err := CreateBridgeToken("BTY", sim, ethRelayer.operatorInfo, x2EthDeployInfo, x2EthContracts, ethRelayer.Addr2TxNonce)
	require.Nil(t, err)
	require.NotEmpty(t, tokenAddrbty)
	sim.Commit()

	addr, err := ethRelayer.ShowTokenAddrBySymbol("BTY")
	require.Nil(t, err)
	assert.Equal(t, addr, tokenAddrbty)

	decimals, err := ethRelayer.GetDecimals(tokenAddrbty)
	require.Nil(t, err)
	assert.Equal(t, decimals, uint8(8))

	_, err = ethRelayer.Burn(para.InitValidators[0].String(), tokenAddrbty, chain33ReceiverAddr, "10")
	require.Error(t, err)

	_, err = ethRelayer.BurnAsync(para.InitValidators[0].String(), tokenAddrbty, chain33ReceiverAddr, "10")
	require.Error(t, err)
}

func Test_BurnBty(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	_, err = ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	tokenAddrbty, err := CreateBridgeToken("bty", sim, ethRelayer.operatorInfo, x2EthDeployInfo, x2EthContracts, ethRelayer.Addr2TxNonce)
	require.Nil(t, err)
	require.NotEmpty(t, tokenAddrbty)
	sim.Commit()

	symbol := &ebTypes.TokenAddress{Symbol: "bty"}
	token, err := ethRelayer.ShowTokenAddress(symbol)
	require.Nil(t, err)
	require.Equal(t, token.TokenAddress[0].Address, tokenAddrbty)

	chain33Sender := []byte("14KEKbYtKKQm4wMthSK9J4La4nAiidGozt")
	amount := int64(100)
	ethReceiver := para.InitValidators[2]
	claimID := crypto.Keccak256Hash(chain33Sender, ethReceiver.Bytes(), big.NewInt(amount).Bytes())
	authOracle, err := ethtxs.PrepareAuth4MultiEthereum(ethRelayer.clientSpec, para.ValidatorPriKey[0], para.InitValidators[0], ethRelayer.Addr2TxNonce)
	require.Nil(t, err)
	signature, err := utils.SignClaim4Evm(claimID, para.ValidatorPriKey[0])
	require.Nil(t, err)

	_, err = x2EthContracts.Oracle.NewOracleClaim(
		authOracle,
		uint8(events.ClaimTypeLock),
		chain33Sender,
		ethReceiver,
		common.HexToAddress(tokenAddrbty),
		"bty",
		big.NewInt(amount),
		claimID,
		signature)
	require.Nil(t, err)
	sim.Commit()

	balanceNew, err := ethRelayer.GetBalance(tokenAddrbty, ethReceiver.String())
	require.Nil(t, err)
	require.Equal(t, balanceNew, "100")

	_, err = ethRelayer.Burn(hexutil.Encode(crypto.FromECDSA(para.ValidatorPriKey[2])), tokenAddrbty, chain33ReceiverAddr, "10")
	require.NoError(t, err)
	sim.Commit()

	balanceNew, err = ethRelayer.GetBalance(tokenAddrbty, ethReceiver.String())
	require.Nil(t, err)
	require.Equal(t, balanceNew, "90")

	// ApproveAllowance
	{
		ownerPrivateKey, err := crypto.ToECDSA(common.FromHex(hexutil.Encode(crypto.FromECDSA(para.ValidatorPriKey[2]))))
		require.Nil(t, err)
		ownerAddr := crypto.PubkeyToAddress(ownerPrivateKey.PublicKey)
		auth, err := ethtxs.PrepareAuth4MultiEthereum(sim, ownerPrivateKey, ownerAddr, ethRelayer.Addr2TxNonce)
		require.Nil(t, err)

		erc20TokenInstance, err := generated.NewBridgeToken(common.HexToAddress(tokenAddrbty), sim)
		require.Nil(t, err)

		bn := big.NewInt(1)
		bn, _ = bn.SetString(utils.TrimZeroAndDot("10"), 10)
		_, err = erc20TokenInstance.Approve(auth, ethRelayer.x2EthDeployInfo.BridgeBank.Address, bn)
		require.Nil(t, err)

		sim.Commit()
	}

	_, err = ethRelayer.BurnAsync(hexutil.Encode(crypto.FromECDSA(para.ValidatorPriKey[2])), tokenAddrbty, chain33ReceiverAddr, "10")
	require.NoError(t, err)
	sim.Commit()

	balanceNew, err = ethRelayer.GetBalance(tokenAddrbty, ethReceiver.String())
	require.Nil(t, err)
	require.Equal(t, balanceNew, "80")

	fetchCnt := int32(10)
	logs, err := ethRelayer.getNextValidEthTxEventLogs(ethRelayer.eventLogIndex.Height, ethRelayer.eventLogIndex.Index, fetchCnt)
	require.NoError(t, err)
	fmt.Println("logs", logs)

	for _, vLog := range logs {
		fmt.Println("*vLog", *vLog)
		ethRelayer.procBridgeBankLogs(*vLog)
	}
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)
}

//func deployContracts() (*ethtxs.DeployPara, *ethinterface.SimExtend, *ethtxs.X2EthContracts, *ethtxs.X2EthDeployInfo, error) {
//	ethValidatorAddrKeys := make([]string, 0)
//	ethValidatorAddrKeys = append(ethValidatorAddrKeys, ethValidatorAddrKeyA)
//	ethValidatorAddrKeys = append(ethValidatorAddrKeys, ethValidatorAddrKeyB)
//	ethValidatorAddrKeys = append(ethValidatorAddrKeys, ethValidatorAddrKeyC)
//	ethValidatorAddrKeys = append(ethValidatorAddrKeys, ethValidatorAddrKeyD)
//	return setup.DeploySpecificContracts(deployerPrivateKey, ethValidatorAddrKeys)
//}

func Test_RestorePrivateKeys(t *testing.T) {
	para, sim, x2EthContracts, x2EthDeployInfo, err := setup.DeployContracts()
	require.NoError(t, err)
	ethRelayer := newEthRelayer(para, sim, x2EthContracts, x2EthDeployInfo)
	_, err = ethRelayer.ImportPrivateKey(passphrase, ethPrivateKeyStr)
	require.Nil(t, err)
	time.Sleep(4 * time.Duration(ethRelayer.fetchHeightPeriodMs) * time.Millisecond)

	go func() {
		for range ethRelayer.unlockchan {
		}
	}()
	ethRelayer.rwLock.RLock()
	temp := ethRelayer.privateKey4Ethereum
	ethRelayer.rwLock.RUnlock()

	err = ethRelayer.RestorePrivateKeys("123")
	ethRelayer.rwLock.RLock()
	assert.NotEqual(t, common.Bytes2Hex(crypto.FromECDSA(temp)), common.Bytes2Hex(crypto.FromECDSA(ethRelayer.privateKey4Ethereum)))
	ethRelayer.rwLock.RUnlock()
	require.Nil(t, err)

	err = ethRelayer.RestorePrivateKeys(passphrase)
	ethRelayer.rwLock.RLock()
	assert.Equal(t, common.Bytes2Hex(crypto.FromECDSA(temp)), common.Bytes2Hex(crypto.FromECDSA(ethRelayer.privateKey4Ethereum)))
	ethRelayer.rwLock.RUnlock()
	require.Nil(t, err)

	err = ethRelayer.StoreAccountWithNewPassphase("new123", passphrase)
	require.Nil(t, err)

	err = ethRelayer.RestorePrivateKeys("new123")
	ethRelayer.rwLock.RLock()
	assert.Equal(t, common.Bytes2Hex(crypto.FromECDSA(temp)), common.Bytes2Hex(crypto.FromECDSA(ethRelayer.privateKey4Ethereum)))
	ethRelayer.rwLock.RUnlock()
	require.Nil(t, err)
}

func newEthRelayer(para *ethtxs.DeployPara, sim *ethinterface.SimExtend, x2EthContracts *ethtxs.X2EthContracts, x2EthDeployInfo *ethtxs.X2EthDeployInfo) *Relayer4Ethereum {
	cfg := initCfg(*configPath)
	cfg.Chain33RelayerCfg.SyncTxConfig.Chain33Host = "http://127.0.0.1:8801"
	cfg.EthRelayerCfg[0].BridgeRegistry = x2EthDeployInfo.BridgeRegistry.Address.String()
	cfg.Chain33RelayerCfg.SyncTxConfig.PushBind = "127.0.0.1:60000"
	cfg.Chain33RelayerCfg.SyncTxConfig.FetchHeightPeriodMs = 50
	cfg.Dbdriver = "memdb"
	cfg.DbPath = "datadirEth"

	db := dbm.NewDB("relayer_db_service", cfg.Dbdriver, cfg.DbPath, cfg.DbCache)
	ethBridgeClaimchan := make(chan *relayerTypes.EthBridgeClaim, 100)
	chain33Msgchan := make(chan *events.Chain33Msg, 100)

	relayer := &Relayer4Ethereum{
		name:                    cfg.EthRelayerCfg[0].EthChainName,
		provider:                cfg.EthRelayerCfg[0].EthProvider,
		db:                      db,
		unlockchan:              make(chan int, 2),
		bridgeRegistryAddr:      x2EthDeployInfo.BridgeRegistry.Address,
		maturityDegree:          1, //cfg.EthRelayerCfg[0].EthMaturityDegree,
		fetchHeightPeriodMs:     1, //cfg.EthRelayerCfg[0].EthBlockFetchPeriod,
		totalTxRelayFromChain33: 0,
		symbol2Addr:             make(map[string]common.Address),
		symbol2LockAddr:         make(map[string]ebTypes.TokenAddress),
		ethBridgeClaimChan:      ethBridgeClaimchan,
		chain33MsgChan:          chain33Msgchan,
		Addr2TxNonce:            make(map[common.Address]*ethtxs.NonceMutex),
	}

	//providerHttp       string
	//rwLock             sync.RWMutex
	//privateKey4Ethereum *ecdsa.PrivateKey
	//ethSender           common.Address
	//processWithDraw     bool
	//bridgeBankAddr          common.Address
	//bridgeBankSub           ethereum.Subscription
	//bridgeBankLog           chan types.Log
	//bridgeBankEventLockSig  string
	//bridgeBankEventBurnSig  string
	//bridgeBankAbi           abi.ABI
	//mulSignAddr             string
	//withdrawFee             map[string]*WithdrawFeeAndQuota

	relayer.eventLogIndex = relayer.getLastBridgeBankProcessedHeight()
	relayer.initBridgeBankTx()
	relayer.clientSpec = sim
	relayer.clientWss = sim
	relayer.clientChainID = big.NewInt(1337)

	relayer.rwLock.Lock()
	relayer.operatorInfo = &ethtxs.OperatorInfo{
		PrivateKey: para.DeployPrivateKey,
		Address:    para.Deployer,
	}
	relayer.deployPara = para
	relayer.x2EthContracts = x2EthContracts
	relayer.x2EthDeployInfo = x2EthDeployInfo
	relayer.rwLock.Unlock()

	relayer.totalTxRelayFromChain33 = relayer.getTotalTxAmount2Eth()
	go relayer.proc()
	return relayer
}

func initCfg(path string) *relayerTypes.RelayerConfig {
	var cfg relayerTypes.RelayerConfig
	if _, err := tml.DecodeFile(path, &cfg); err != nil {
		os.Exit(-1)
	}
	return &cfg
}
