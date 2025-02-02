package crosschain

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/scroll-tech/chain-monitor/internal/logic/slack"
	"github.com/scroll-tech/chain-monitor/internal/orm"
	"github.com/scroll-tech/chain-monitor/internal/types"
)

const ethBalanceGap = 50

// LogicMessengerCrossChain check messenger balance match
type LogicMessengerCrossChain struct {
	db                  *gorm.DB
	messengerMessageOrm *orm.MessengerMessageMatch
	l1Client            *ethclient.Client
	l2Client            *ethclient.Client
	l1MessengerAddr     common.Address
	l2MessengerAddr     common.Address
	checker             *MessengerCrossEventMatcher

	crossChainETHTotal    *prometheus.CounterVec
	startMessengerBalance uint64
}

// NewLogicMessengerCrossChain is a constructor for Logic.
func NewLogicMessengerCrossChain(db *gorm.DB, l1Client, l2Client *ethclient.Client, l1MessengerAddr, l2MessengerAddr common.Address, startMessengerBalance uint64) *LogicMessengerCrossChain {
	return &LogicMessengerCrossChain{
		db:                    db,
		messengerMessageOrm:   orm.NewMessengerMessageMatch(db),
		l1Client:              l1Client,
		l2Client:              l2Client,
		l1MessengerAddr:       l1MessengerAddr,
		l2MessengerAddr:       l2MessengerAddr,
		checker:               NewMessengerCrossEventMatcher(),
		startMessengerBalance: startMessengerBalance,

		crossChainETHTotal: promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "cross_chain_checked_eth_total",
			Help: "the total of cross chain eth checked",
		}, []string{"layer"}),
	}
}

// CheckETHBalance checks the ETH balance for the given Ethereum layer (either Layer1 or Layer2).
func (c *LogicMessengerCrossChain) CheckETHBalance(ctx context.Context, layerType types.LayerType) {
	log.Info("CheckETHBalance started", "layer type", layerType)

	latestBlockNumber, err := c.getLatestBlockNumber(ctx, layerType)
	if err != nil {
		log.Error("get latest block number from geth node failed", "layer", layerType, "error", err)
		return
	}

	startBalance, err := c.messengerMessageOrm.GetETHCheckStartBlockNumberAndBalance(ctx, layerType)
	if err != nil {
		log.Error("c.messageOrm GetETHCheckStartBlockNumberAndBalance failed", "layer type", layerType, "error", err)
		return
	}

	if startBalance == nil {
		if layerType == types.Layer2 {
			startBalance, err = c.l2Client.BalanceAt(ctx, c.l2MessengerAddr, new(big.Int).SetUint64(0))
			if err != nil {
				log.Error("get messenger balance failed", "layer types", layerType, "err", err)
				return
			}
		}

		if layerType == types.Layer1 {
			startBalance = new(big.Int).SetUint64(c.startMessengerBalance)
			log.Info("L1 messenger start balance", "start", startBalance.String())
		}
	}

	messageLimit := 1000
	messages, err := c.messengerMessageOrm.GetUncheckedLatestETHMessageMatch(ctx, layerType, messageLimit)
	if err != nil {
		log.Error("CheckETHBalance.GetUncheckedLatestETHMessageMatch failed", "limit", messageLimit, "error", err)
		return
	}

	if len(messages) == 0 {
		return
	}

	var startBlockNumber, endBlockNumber uint64
	if layerType == types.Layer1 {
		startBlockNumber = messages[0].L1BlockNumber
		endBlockNumber = messages[len(messages)-1].L1BlockNumber
	} else {
		startBlockNumber = messages[0].L2BlockNumber
		endBlockNumber = messages[len(messages)-1].L2BlockNumber
	}

	messageMatches, err := c.messengerMessageOrm.GetETHMessageMatchByBlockRange(ctx, layerType, startBlockNumber, endBlockNumber)
	if err != nil {
		log.Error("CheckETHBalance.GetETHMessageMatchByBlockRange failed", "start", startBlockNumber, "end", endBlockNumber, "error", err)
		return
	}

	var truncateBlockNumber uint64
	for _, messageMatch := range messageMatches {
		if types.ETHAmountStatus(messageMatch.ETHAmountStatus) != types.ETHAmountStatusTypeSet {
			if layerType == types.Layer1 {
				truncateBlockNumber = messageMatch.L1BlockNumber
			} else {
				truncateBlockNumber = messageMatch.L2BlockNumber
			}
			break
		}
	}

	var truncatedMessageMatches []*orm.MessengerMessageMatch
	for _, message := range messageMatches {
		if truncateBlockNumber == 0 { // not need to truncate.
			truncatedMessageMatches = append(truncatedMessageMatches, message)
			continue
		}
		if layerType == types.Layer1 {
			if message.L1BlockNumber < truncateBlockNumber {
				truncatedMessageMatches = append(truncatedMessageMatches, message)
			}
		} else {
			if message.L2BlockNumber < truncateBlockNumber {
				truncatedMessageMatches = append(truncatedMessageMatches, message)
			}
		}
	}

	if len(truncatedMessageMatches) == 0 {
		return
	}

	switch layerType {
	case types.Layer1:
		startBlockNumber = truncatedMessageMatches[0].L1BlockNumber
		endBlockNumber = truncatedMessageMatches[len(truncatedMessageMatches)-1].L1BlockNumber
	case types.Layer2:
		startBlockNumber = truncatedMessageMatches[0].L2BlockNumber
		endBlockNumber = truncatedMessageMatches[len(truncatedMessageMatches)-1].L2BlockNumber
	}

	c.checkETH(ctx, layerType, startBlockNumber, endBlockNumber, latestBlockNumber, startBalance, truncatedMessageMatches)
	log.Info("CheckETHBalance completed", "layer type", layerType, "start", startBlockNumber, "end", endBlockNumber)
}

func (c *LogicMessengerCrossChain) checkETH(ctx context.Context, layer types.LayerType, startBlockNumber, endBlockNumber, latestBlockNumber uint64, startBalance *big.Int, messages []*orm.MessengerMessageMatch) {
	var messengerAddr common.Address
	var client *ethclient.Client
	if layer == types.Layer1 {
		messengerAddr = c.l1MessengerAddr
		client = c.l1Client
	} else {
		messengerAddr = c.l2MessengerAddr
		client = c.l2Client
	}

	log.Info("checking eth balance", "start", startBlockNumber, "end", endBlockNumber, "latest", latestBlockNumber)

	// because balanceAt can't get the too early block balance, so only can compute the locally l1 messenger balance and
	// update the l1_messenger_eth_balance/l2_messenger_eth_balance
	if layer == types.Layer1 && endBlockNumber+ethBalanceGap < latestBlockNumber {
		c.computeBlockBalance(ctx, layer, messages, startBalance)
		return
	}

	endBalance, err := client.BalanceAt(ctx, messengerAddr, new(big.Int).SetUint64(endBlockNumber))
	if err != nil {
		log.Error("get messenger balance failed", "layer types", layer, "addr", messengerAddr, "end", endBlockNumber, "err", err)
		return
	}

	ok, expectedEndBalance, actualBalance, err := c.checkBalance(layer, startBalance, endBalance, messages)
	if err != nil {
		log.Error("checkLayer1Balance failed", "startBlock", startBlockNumber, "endBlock", endBlockNumber, "expectedEndBalance", expectedEndBalance, "actualBalance", actualBalance, "err", err)
		return
	}

	if !ok {
		c.checkBlockBalanceOneByOne(ctx, client, messengerAddr, layer, messages)
		return
	}

	// get all the eth status valid, and update the eth balance status and eth balance
	c.computeBlockBalance(ctx, layer, messages, startBalance)
}

func (c *LogicMessengerCrossChain) checkBlockBalanceOneByOne(ctx context.Context, client *ethclient.Client, messengerAddr common.Address, layer types.LayerType, messages []*orm.MessengerMessageMatch) {
	var startBalance *big.Int
	var startIndex int
	for idx, message := range messages {
		c.checker.MessengerCrossChainCheck(layer, message)

		var blockNumber uint64
		if layer == types.Layer1 {
			blockNumber = message.L1BlockNumber
		} else {
			blockNumber = message.L2BlockNumber
		}

		tmpBalance, err := client.BalanceAt(ctx, messengerAddr, new(big.Int).SetUint64(blockNumber))
		if err != nil {
			log.Error("get balance failed", "block number", blockNumber, "err", err)
			continue
		}

		startBalance = tmpBalance
		startIndex = idx
		break
	}

	for i := startIndex + 1; i < len(messages); i++ {
		var blockNumber uint64
		if layer == types.Layer1 {
			blockNumber = messages[i].L1BlockNumber
		} else {
			blockNumber = messages[i].L2BlockNumber
		}

		if layer == types.Layer1 && (i+1 != len(messages) && (messages[i].L1BlockNumber == messages[i+1].L1BlockNumber)) {
			continue
		}

		if layer == types.Layer2 && (i+1 != len(messages) && (messages[i].L2BlockNumber == messages[i+1].L2BlockNumber)) {
			continue
		}

		endBalance, err := client.BalanceAt(ctx, messengerAddr, new(big.Int).SetUint64(blockNumber))
		if err != nil {
			continue
		}

		ok, expectedEndBalance, actualBalance, err := c.checkBalance(layer, startBalance, endBalance, messages[startIndex:i+1])
		if !ok || err != nil {
			log.Error("balance check failed", "block", blockNumber, "expectedEndBalance", expectedEndBalance.String(), "actualBalance", actualBalance.String())
			slack.MrkDwnETHGatewayMessage(messages[i], expectedEndBalance, actualBalance)
			continue
		}
	}
}

func (c *LogicMessengerCrossChain) checkBalance(layer types.LayerType, startBalance, endBalance *big.Int, messages []*orm.MessengerMessageMatch) (bool, *big.Int, *big.Int, error) {
	balanceDiff := big.NewInt(0)
	for _, message := range messages {
		c.crossChainETHTotal.WithLabelValues(layer.String()).Inc()

		var amount *big.Int
		var ok bool
		amount, ok = new(big.Int).SetString(message.ETHAmount, 10)
		if !ok {
			return false, nil, nil, fmt.Errorf("database id:%d invalid ETHAmount value: %v, layer: %v", message.ID, message.ETHAmount, layer)
		}

		if layer == types.Layer1 {
			if types.EventType(message.L1EventType) == types.L1SentMessage {
				balanceDiff = new(big.Int).Add(balanceDiff, amount)
			}

			if types.EventType(message.L1EventType) == types.L1RelayedMessage {
				balanceDiff = new(big.Int).Sub(balanceDiff, amount)
			}
		}

		if layer == types.Layer2 {
			if types.EventType(message.L2EventType) == types.L2SentMessage {
				balanceDiff = new(big.Int).Add(balanceDiff, amount)
			}

			if types.EventType(message.L2EventType) == types.L2RelayedMessage {
				balanceDiff = new(big.Int).Sub(balanceDiff, amount)
			}
		}
	}

	expectedEndBalance := new(big.Int).Add(startBalance, balanceDiff)
	if expectedEndBalance.Cmp(endBalance) == 0 {
		return true, expectedEndBalance, endBalance, nil
	}

	log.Error("balance check failed", "expectedEndBalance", expectedEndBalance.String(), "actualBalance", endBalance.String())
	return false, expectedEndBalance, endBalance, nil
}

func (c *LogicMessengerCrossChain) computeBlockBalance(ctx context.Context, layer types.LayerType, messages []*orm.MessengerMessageMatch, messengerETHBalance *big.Int) {
	blockNumberAmountMap := make(map[uint64]*big.Int)
	for _, message := range messages {
		c.checker.MessengerCrossChainCheck(layer, message)

		if layer == types.Layer1 {
			if _, ok := blockNumberAmountMap[message.L1BlockNumber]; !ok {
				blockNumberAmountMap[message.L1BlockNumber] = new(big.Int)
			}

			amount, ok := new(big.Int).SetString(message.ETHAmount, 10)
			if !ok {
				log.Error("invalid L1 ETH Amount value", "amount", message.ETHAmount)
				return
			}

			if types.EventType(message.L1EventType) == types.L1SentMessage {
				blockNumberAmountMap[message.L1BlockNumber] = new(big.Int).Add(blockNumberAmountMap[message.L1BlockNumber], amount)
			}

			if types.EventType(message.L1EventType) == types.L1RelayedMessage {
				blockNumberAmountMap[message.L1BlockNumber] = new(big.Int).Sub(blockNumberAmountMap[message.L1BlockNumber], amount)
			}
		}

		if layer == types.Layer2 {
			if _, ok := blockNumberAmountMap[message.L2BlockNumber]; !ok {
				blockNumberAmountMap[message.L2BlockNumber] = new(big.Int)
			}

			amount, ok := new(big.Int).SetString(message.ETHAmount, 10)
			if !ok {
				log.Error("invalid L2 ETH Amount value", "amount", message.ETHAmount)
				return
			}

			if types.EventType(message.L2EventType) == types.L2SentMessage {
				blockNumberAmountMap[message.L2BlockNumber] = new(big.Int).Add(blockNumberAmountMap[message.L2BlockNumber], amount)
			}

			if types.EventType(message.L2EventType) == types.L2RelayedMessage {
				blockNumberAmountMap[message.L2BlockNumber] = new(big.Int).Sub(blockNumberAmountMap[message.L2BlockNumber], amount)
			}
		}
	}

	var updateETHMessageMatches []orm.MessengerMessageMatch
	lastBlockBalance := new(big.Int).Set(messengerETHBalance)
	lastBlockNumber := uint64(0)
	for _, v := range messages {
		blockNumber := v.L1BlockNumber
		if layer == types.Layer2 {
			blockNumber = v.L2BlockNumber
		}

		if blockNumber != lastBlockNumber {
			lastBlockBalance.Add(lastBlockBalance, blockNumberAmountMap[blockNumber])
			lastBlockNumber = blockNumber
		}

		// update the db
		mm := orm.MessengerMessageMatch{ID: v.ID}
		if layer == types.Layer1 {
			mm.L1MessengerETHBalance = decimal.NewFromBigInt(lastBlockBalance, 0)
			mm.L1ETHBalanceStatus = int(types.ETHBalanceStatusTypeValid)
		} else {
			mm.L2MessengerETHBalance = decimal.NewFromBigInt(lastBlockBalance, 0)
			mm.L2ETHBalanceStatus = int(types.ETHBalanceStatusTypeValid)
		}
		updateETHMessageMatches = append(updateETHMessageMatches, mm)
	}

	// Sort the updateETHMessageMatches slice by id to prevent "ERROR: deadlock detected (SQLSTATE 40P01)"
	// when simultaneously updating rows of postgres in a transaction by L1 & L2 eth balance checkers.
	sort.Slice(updateETHMessageMatches, func(i, j int) bool {
		return updateETHMessageMatches[i].ID < updateETHMessageMatches[j].ID
	})

	err := c.db.Transaction(func(tx *gorm.DB) error {
		for _, updateEthMessageMatch := range updateETHMessageMatches {
			if err := c.messengerMessageOrm.UpdateETHBalance(ctx, layer, updateEthMessageMatch, tx); err != nil {
				log.Error("computeOverageBlockBalance.UpdateETHBalance failed", "layer", layer, "message match:%v", updateEthMessageMatch, "error", err)
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Error("computeOverageBlockBalance.UpdateETHBalance failed", "layer", layer, "error", err)
	}
}

func (c *LogicMessengerCrossChain) getLatestBlockNumber(ctx context.Context, layerType types.LayerType) (uint64, error) {
	switch layerType {
	case types.Layer1:
		latestHeader, err := c.l1Client.HeaderByNumber(ctx, nil)
		if err != nil {
			log.Error("Failed to get latest header from Layer1 client", "error", err)
			return 0, err
		}
		return latestHeader.Number.Uint64(), nil

	case types.Layer2:
		latestHeader, err := c.l2Client.HeaderByNumber(ctx, nil)
		if err != nil {
			log.Error("Failed to get latest header from Layer2 client", "error", err)
			return 0, err
		}
		return latestHeader.Number.Uint64(), nil

	default:
		log.Error("Invalid layerType", "layerType", layerType)
		return 0, fmt.Errorf("invalid layerType: %v", layerType)
	}
}
