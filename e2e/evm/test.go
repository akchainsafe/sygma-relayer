package evm

import (
	"context"
	"math/big"
	"time"

	"github.com/ChainSafe/chainbridge-core/chains/evm/calls"
	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/transactor"
	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/transactor/signAndSend"
	"github.com/ChainSafe/chainbridge-core/e2e/dummy"
	substrateTypes "github.com/centrifuge/go-substrate-rpc-client/types"
	"github.com/ethereum/go-ethereum/common"

	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/contracts/bridge"
	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/contracts/centrifuge"
	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/contracts/erc20"
	"github.com/ChainSafe/chainbridge-core/chains/evm/calls/contracts/erc721"

	"github.com/ChainSafe/chainbridge-core/chains/evm/cli/local"
	"github.com/ChainSafe/chainbridge-core/keystore"
	"github.com/ethereum/go-ethereum"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/suite"
)

var (
	ber                = "1000.0"
	ter                = "1000.0"
	destGasPrice       = big.NewInt(1000000000)
	expireTimestamp    = time.Now().Unix() + 3600
	fromDomainID       = uint8(1)
	destDomainID       = uint8(2)
	erc20TokenDecimals = int64(18)
	etherDecimals      = int64(18)
)

type TestClient interface {
	local.E2EClient
	LatestBlock() (*big.Int, error)
	FetchEventLogs(ctx context.Context, contractAddress common.Address, event string, startBlock *big.Int, endBlock *big.Int) ([]types.Log, error)
	SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
	TransactionByHash(ctx context.Context, hash common.Hash) (tx *types.Transaction, isPending bool, err error)
}

func SetupEVM2EVMTestSuite(fabric1, fabric2 calls.TxFabric, client1, client2 TestClient) *IntegrationTestSuite {
	return &IntegrationTestSuite{
		fabric1:  fabric1,
		fabric2:  fabric2,
		client1:  client1,
		client2:  client2,
		basicFee: big.NewInt(1000000000),
	}
}

type IntegrationTestSuite struct {
	suite.Suite
	client1    TestClient
	client2    TestClient
	gasPricer1 calls.GasPricer
	gasPricer2 calls.GasPricer
	fabric1    calls.TxFabric
	fabric2    calls.TxFabric
	erc20RID   [32]byte
	erc721RID  [32]byte
	genericRID [32]byte
	config1    local.EVME2EConfig
	config2    local.EVME2EConfig
	basicFee   *big.Int
}

func (s *IntegrationTestSuite) SetupSuite() {
	config1, err := local.PrepareLocalEVME2EEnv(s.client1, s.fabric1, 1, s.client1.From())
	if err != nil {
		panic(err)
	}
	s.config1 = config1

	config2, err := local.PrepareLocalEVME2EEnv(s.client2, s.fabric2, 2, s.client2.From())
	if err != nil {
		panic(err)
	}
	s.config2 = config2

	s.erc20RID = calls.SliceTo32Bytes(common.LeftPadBytes([]byte{0}, 31))
	s.genericRID = calls.SliceTo32Bytes(common.LeftPadBytes([]byte{1}, 31))
	s.erc721RID = calls.SliceTo32Bytes(common.LeftPadBytes([]byte{2}, 31))
	s.gasPricer1 = dummy.NewStaticGasPriceDeterminant(s.client1, nil)
	s.gasPricer2 = dummy.NewStaticGasPriceDeterminant(s.client2, nil)
}
func (s *IntegrationTestSuite) TearDownSuite() {}
func (s *IntegrationTestSuite) SetupTest()     {}
func (s *IntegrationTestSuite) TearDownTest()  {}

func (s *IntegrationTestSuite) TestErc20Deposit() {
	dstAddr := keystore.TestKeyRing.EthereumKeys[keystore.BobKey].CommonAddress()

	transactor1 := signAndSend.NewSignAndSendTransactor(s.fabric1, s.gasPricer1, s.client1)
	erc20Contract1 := erc20.NewERC20Contract(s.client1, s.config1.Erc20Addr, transactor1)
	bridgeContract1 := bridge.NewBridgeContract(s.client1, s.config1.BridgeAddr, transactor1)

	transactor2 := signAndSend.NewSignAndSendTransactor(s.fabric2, s.gasPricer2, s.client2)
	erc20Contract2 := erc20.NewERC20Contract(s.client2, s.config2.Erc20Addr, transactor2)

	senderBalBefore, err := erc20Contract1.GetBalance(local.EveKp.CommonAddress())
	s.Nil(err)
	destBalanceBefore, err := erc20Contract2.GetBalance(dstAddr)
	s.Nil(err)

	amountToDeposit := big.NewInt(1000000)

	depositTxHash, err := bridgeContract1.Erc20Deposit(dstAddr, amountToDeposit, s.erc20RID,
		ber, ter, destGasPrice, expireTimestamp, fromDomainID, destDomainID, erc20TokenDecimals, etherDecimals,
		nil, false, transactor.TransactOptions{
			Priority: uint8(2), // fast
			Value:    s.basicFee,
		})
	s.Nil(err)

	depositTx, _, err := s.client1.TransactionByHash(context.Background(), *depositTxHash)
	s.Nil(err)
	// check gas price of deposit tx - 140 gwei
	s.Equal(big.NewInt(140000000000), depositTx.GasPrice())

	err = WaitForProposalExecuted(s.client2, s.config2.BridgeAddr)
	s.Nil(err)

	senderBalAfter, err := erc20Contract1.GetBalance(s.client1.From())
	s.Nil(err)
	s.Equal(-1, senderBalAfter.Cmp(senderBalBefore))

	destBalanceAfter, err := erc20Contract2.GetBalance(dstAddr)
	s.Nil(err)
	//Balance has increased
	s.Equal(1, destBalanceAfter.Cmp(destBalanceBefore))
}

func (s *IntegrationTestSuite) TestErc721Deposit() {
	tokenId := big.NewInt(1)
	metadata := "metadata.url"

	dstAddr := keystore.TestKeyRing.EthereumKeys[keystore.BobKey].CommonAddress()

	txOptions := transactor.TransactOptions{
		Priority: uint8(2), // fast
	}

	// erc721 contract for evm1
	transactor1 := signAndSend.NewSignAndSendTransactor(s.fabric1, s.gasPricer1, s.client1)
	erc721Contract1 := erc721.NewErc721Contract(s.client1, s.config1.Erc721Addr, transactor1)
	bridgeContract1 := bridge.NewBridgeContract(s.client1, s.config1.BridgeAddr, transactor1)

	// erc721 contract for evm2
	transactor2 := signAndSend.NewSignAndSendTransactor(s.fabric2, s.gasPricer2, s.client2)
	erc721Contract2 := erc721.NewErc721Contract(s.client2, s.config2.Erc721Addr, transactor2)

	// Mint token and give approval
	// This is done here so token only exists on evm1
	_, err := erc721Contract1.Mint(tokenId, metadata, s.client1.From(), txOptions)
	s.Nil(err, "Mint failed")
	_, err = erc721Contract1.Approve(tokenId, s.config1.Erc721HandlerAddr, txOptions)
	s.Nil(err, "Approve failed")

	// Check on evm1 if initial owner is admin
	initialOwner, err := erc721Contract1.Owner(tokenId)
	s.Nil(err)
	s.Equal(initialOwner.String(), s.client1.From().String())

	// Check on evm2 token doesn't exist
	_, err = erc721Contract2.Owner(tokenId)
	s.Error(err)

	depositTxHash, err := bridgeContract1.Erc721Deposit(
		tokenId, metadata, dstAddr, s.erc721RID,
		ber, ter, destGasPrice, expireTimestamp, fromDomainID, destDomainID, erc20TokenDecimals, etherDecimals,
		nil, false, transactor.TransactOptions{
			Value: s.basicFee,
		},
	)
	s.Nil(err)

	depositTx, _, err := s.client1.TransactionByHash(context.Background(), *depositTxHash)
	s.Nil(err)
	// check gas price of deposit tx - 50 gwei (slow)
	s.Equal(big.NewInt(50000000000), depositTx.GasPrice())

	err = WaitForProposalExecuted(s.client2, s.config2.BridgeAddr)
	s.Nil(err)

	// Check on evm1 that token is burned
	_, err = erc721Contract1.Owner(tokenId)
	s.Error(err)

	// Check on evm2 that token is minted to destination address
	owner, err := erc721Contract2.Owner(tokenId)
	s.Nil(err)
	s.Equal(dstAddr.String(), owner.String())
}

func (s *IntegrationTestSuite) TestGenericDeposit() {
	transactor1 := signAndSend.NewSignAndSendTransactor(s.fabric1, s.gasPricer1, s.client1)
	transactor2 := signAndSend.NewSignAndSendTransactor(s.fabric2, s.gasPricer2, s.client2)

	bridgeContract1 := bridge.NewBridgeContract(s.client1, s.config1.BridgeAddr, transactor1)
	assetStoreContract2 := centrifuge.NewAssetStoreContract(s.client2, s.config2.AssetStoreAddr, transactor2)

	hash, _ := substrateTypes.GetHash(substrateTypes.NewI64(int64(1)))

	depositTxHash, err := bridgeContract1.GenericDeposit(hash[:], s.genericRID, ber, ter, destGasPrice, expireTimestamp,
		fromDomainID, destDomainID, erc20TokenDecimals, etherDecimals, nil, false, transactor.TransactOptions{
			Priority: uint8(0), // slow
			Value:    s.basicFee,
		})
	s.Nil(err)

	depositTx, _, err := s.client1.TransactionByHash(context.Background(), *depositTxHash)
	s.Nil(err)
	// check gas price of deposit tx - 140 gwei
	s.Equal(big.NewInt(50000000000), depositTx.GasPrice())

	err = WaitForProposalExecuted(s.client2, s.config2.BridgeAddr)
	s.Nil(err)
	// Asset hash sent is stored in centrifuge asset store contract
	exists, err := assetStoreContract2.IsCentrifugeAssetStored(hash)
	s.Nil(err)
	s.Equal(true, exists)
}
