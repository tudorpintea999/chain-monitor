package orm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/scroll-tech/go-ethereum/log"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/scroll-tech/chain-monitor/internal/types"
)

// MessageMatch contains the tx of l1 & l2
type MessageMatch struct {
	db *gorm.DB `gorm:"column:-"`

	ID          int64  `json:"id" gorm:"column:id"`
	MessageHash string `json:"message_hash" gorm:"message_hash"`
	TokenType   int    `json:"token_type" gorm:"token_type"`

	// l1 event info
	L1EventType   int    `json:"l1_event_type" gorm:"l1_event_type"`
	L1BlockNumber uint64 `json:"l1_block_number" gorm:"l1_block_number"`
	L1TxHash      string `json:"l1_tx_hash" gorm:"l1_tx_hash"`
	L1TokenIds    string `json:"l1_token_ids" gorm:"l1_token_ids"`
	L1Amounts     string `json:"l1_amounts" gorm:"l1_amounts"`

	// l2 event info
	L2EventType   int    `json:"l2_event_type" gorm:"l2_event_type"`
	L2BlockNumber uint64 `json:"l2_block_number" gorm:"l2_block_number"`
	L2TxHash      string `json:"l2_tx_hash" gorm:"l2_tx_hash"`
	L2TokenIds    string `json:"l2_token_ids" gorm:"l2_token_ids"`
	L2Amounts     string `json:"l2_amounts" gorm:"l2_amounts"`

	// eth event info
	L1MessengerETHBalance decimal.Decimal `json:"l1_messenger_eth_balance" gorm:"l1_messenger_eth_balance"`
	L1ETHBalanceStatus    int             `json:"l1_eth_balance_status" gorm:"l1_eth_balance_status"`
	L2MessengerETHBalance decimal.Decimal `json:"l2_messenger_eth_balance" gorm:"l2_messenger_eth_balance"`
	L2ETHBalanceStatus    int             `json:"l2_eth_balance_status" gorm:"l2_eth_balance_status"`

	// status
	CheckStatus        int    `json:"check_status" gorm:"check_status"`
	L1BlockStatus      int    `json:"l1_block_status" gorm:"l1_block_status"`
	L2BlockStatus      int    `json:"l2_block_status" gorm:"l2_block_status"`
	L1CrossChainStatus int    `json:"l1_cross_chain_status" gorm:"l1_cross_chain_status"`
	L2CrossChainStatus int    `json:"l2_cross_chain_status" gorm:"l2_cross_chain_status"`
	MessageProof       []byte `json:"message_proof" gorm:"message_proof"` // only not null in the last message of each block.
	MessageNonce       uint64 `json:"message_nonce" gorm:"message_nonce"` // only not null in the last message of each block.

	CreatedAt time.Time      `json:"created_at" gorm:"column:created_at"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"column:deleted_at"`
}

// NewMessageMatch creates a new MessageMatch database instance.
func NewMessageMatch(db *gorm.DB) *MessageMatch {
	return &MessageMatch{db: db}
}

// TableName returns the table name for the Batch model.
func (*MessageMatch) TableName() string {
	return "message_match"
}

// GetUncheckedAndDoubleLayerValidGatewayMessageMatchs retrieves the earliest unchecked gateway message match records
// that are valid in both Layer1 and Layer2.
func (m *MessageMatch) GetUncheckedAndDoubleLayerValidGatewayMessageMatchs(ctx context.Context, limit int) ([]MessageMatch, error) {
	var messages []MessageMatch
	db := m.db.WithContext(ctx)
	db = db.Where("l1_block_status = ?", types.BlockStatusTypeValid)
	db = db.Where("l2_block_status = ?", types.BlockStatusTypeValid)
	db = db.Where("check_status = ?", types.CheckStatusUnchecked)
	db = db.Order("id asc")
	db = db.Limit(limit)
	if err := db.Find(&messages).Error; err != nil {
		log.Warn("MessageMatch.GetUncheckedAndDoubleLayerValidGatewayMessageMatchs failed", "error", err)
		return nil, fmt.Errorf("MessageMatch.GetUncheckedAndDoubleLayerValidGatewayMessageMatchs failed err:%w", err)
	}
	return messages, nil
}

// GetUncheckedLatestETHMessageMatch get the latest uncheck eth message match record
func (m *MessageMatch) GetUncheckedLatestETHMessageMatch(ctx context.Context, layer types.LayerType, limit int) ([]MessageMatch, error) {
	var messages []MessageMatch
	db := m.db.WithContext(ctx)
	switch layer {
	case types.Layer1:
		db = db.Where("l1_eth_balance_status = ?", types.ETHBalanceStatusTypeInvalid)
	case types.Layer2:
		db = db.Where("l2_eth_balance_status = ?", types.ETHBalanceStatusTypeInvalid)
	}
	db = db.Where("token_type = ", types.TokenTypeETH)
	db = db.Order("id asc")
	db = db.Limit(limit)
	if err := db.Find(&messages).Error; err != nil {
		log.Warn("MessageMatch.GetUncheckedLatestETHMessageMatch failed", "error", err)
		return nil, fmt.Errorf("MessageMatch.GetUncheckedLatestETHMessageMatch failed err:%w", err)
	}
	return messages, nil
}

// GetLatestBlockValidMessageMatch fetches the latest valid message match record for the specified layer.
func (m *MessageMatch) GetLatestBlockValidMessageMatch(ctx context.Context, layer types.LayerType) (*MessageMatch, error) {
	var message MessageMatch
	db := m.db.WithContext(ctx)
	switch layer {
	case types.Layer1:
		db = db.Where("l1_block_status = ?", types.BlockStatusTypeValid)
	case types.Layer2:
		db = db.Where("l2_block_status = ?", types.BlockStatusTypeValid)
	}
	err := db.Last(&message).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("MessageMatch.GetLatestBlockValidMessageMatch failed", "error", err)
		return nil, fmt.Errorf("MessageMatch.GetLatestBlockValidMessageMatch failed err:%w", err)
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &message, nil
}

// GetLatestDoubleLayerValidMessageMatch fetches the latest valid message match record where both layers are valid.
func (m *MessageMatch) GetLatestDoubleLayerValidMessageMatch(ctx context.Context) (*MessageMatch, error) {
	var message MessageMatch
	db := m.db.WithContext(ctx)

	// Look for records where both layers are valid
	db = db.Where("l1_block_status = ?", types.BlockStatusTypeValid)
	db = db.Where("l2_block_status = ?", types.BlockStatusTypeValid)

	err := db.Last(&message).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("MessageMatch.GetLatestDoubleLayerValidMessageMatch failed", "error", err)
		return nil, fmt.Errorf("MessageMatch.GetLatestDoubleLayerValidMessageMatch failed err:%w", err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &message, nil
}

// GetLatestValidETHBalanceMessageMatch fetches the latest valid Ethereum balance match record for the specified layer.
func (m *MessageMatch) GetLatestValidETHBalanceMessageMatch(ctx context.Context, layer types.LayerType) (*MessageMatch, error) {
	var message MessageMatch
	db := m.db.WithContext(ctx)
	switch layer {
	case types.Layer1:
		db = db.Where("l1_eth_balance_status = ?", types.ETHBalanceStatusTypeValid)
	case types.Layer2:
		db = db.Where("l2_eth_balance_status = ?", types.ETHBalanceStatusTypeValid)
	}
	err := db.Last(&message).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("MessageMatch.GetLatestBlockValidMessageMatch failed", "error", err)
		return nil, fmt.Errorf("MessageMatch.GetLatestBlockValidMessageMatch failed err:%w", err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &message, nil
}

// GetLargestMessageNonceL2MessageMatch fetches the message match record with the maximum MessageNonce.
func (m *MessageMatch) GetLargestMessageNonceL2MessageMatch(ctx context.Context) (*MessageMatch, error) {
	var message MessageMatch
	db := m.db.WithContext(ctx)
	db = db.Where("message_nonce > ?", 0)
	db = db.Order("id DESC")
	err := db.First(&message).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		log.Warn("GetLargestMessageNonceL2MessageMatch failed", "error", err)
		return nil, fmt.Errorf("GetLargestMessageNonceL2MessageMatch failed, err:%w", err)
	}
	return &message, nil
}

// InsertOrUpdateMsgProofNonce insert or update the withdrawal tree root's message proof and nonce
func (m *MessageMatch) InsertOrUpdateMsgProofNonce(ctx context.Context, messages []MessageMatch) (int64, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})
	db = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "message_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"message_proof", "message_nonce"}),
	})
	result := db.Create(&messages)
	if result.Error != nil {
		return 0, fmt.Errorf("MessageMatch.InsertOrUpdateMsgProofNonce error: %w, messages: %v", result.Error, messages)
	}
	return result.RowsAffected, nil
}

// InsertOrUpdateGatewayEventInfo insert or update eth event info
func (m *MessageMatch) InsertOrUpdateGatewayEventInfo(ctx context.Context, layer types.LayerType, messages MessageMatch) (int64, error) {
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})

	var assignmentColumn clause.Set
	if layer == types.Layer1 {
		assignmentColumn = clause.AssignmentColumns([]string{"token_type", "l1_event_type", "l1_token_ids", "l1_amounts"})
	} else if layer == types.Layer2 {
		assignmentColumn = clause.AssignmentColumns([]string{"token_type", "l2_event_type", "l2_token_ids", "l2_amounts"})
	}

	db = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "message_hash"}},
		DoUpdates: assignmentColumn,
	})

	result := db.Create(&messages)
	if result.Error != nil {
		return 0, fmt.Errorf("MessageMatch.InsertOrUpdateGatewayEventInfo error: %w, messages: %v", result.Error, messages)
	}
	return result.RowsAffected, nil
}

// InsertOrUpdateETHEventInfo insert or update the eth event info
func (m *MessageMatch) InsertOrUpdateETHEventInfo(ctx context.Context, message MessageMatch) (int64, error) {
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})
	var columns []string
	if message.L1EventType != 0 && message.L1EventType == int(types.L1SentMessage) {
		columns = append(columns, "l1_event_type", "l1_block_number", "l1_tx_hash", "l1_token_ids", "l1_amounts", "l2_amounts")
	}

	if message.L1EventType != 0 && message.L1EventType == int(types.L1RelayedMessage) {
		columns = append(columns, "l1_event_type", "l1_block_number", "l1_tx_hash", "l1_token_ids")
	}

	if message.L2EventType != 0 && message.L2EventType == int(types.L2SentMessage) {
		columns = append(columns, "l2_event_type", "l2_block_number", "l2_tx_hash", "l2_token_ids", "l1_amounts", "l2_amounts")
	}

	if message.L2EventType != 0 && message.L2EventType == int(types.L2RelayedMessage) {
		columns = append(columns, "l2_event_type", "l2_block_number", "l2_tx_hash", "l2_token_ids")
	}

	db = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "message_hash"}},
		DoUpdates: clause.AssignmentColumns(columns),
	})

	result := db.Create(&message)
	if result.Error != nil {
		return 0, fmt.Errorf("MessageMatch.InsertOrUpdateETHEventInfo error: %w, message: %v", result.Error, message)
	}
	return result.RowsAffected, nil
}

// UpdateBlockStatus updates the block status for the given layer and block number range.
func (m *MessageMatch) UpdateBlockStatus(ctx context.Context, layer types.LayerType, startBlockNumber, endBlockNumber uint64) error {
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})

	switch layer {
	case types.Layer1:
		db = db.Where("l1_block_status = ?", types.BlockStatusTypeInvalid)
		db = db.Where("l1_block_number >= ? AND l1_block_number <= ?", startBlockNumber, endBlockNumber)
		db = db.Update("l1_block_status", types.BlockStatusTypeValid)
	case types.Layer2:
		db = db.Where("l2_block_status = ?", types.BlockStatusTypeInvalid)
		db = db.Where("l2_block_number >= ? AND l2_block_number <= ?", startBlockNumber, endBlockNumber)
		db = db.Update("l2_block_status", types.BlockStatusTypeValid)
	}

	if db.Error != nil {
		log.Warn("MessageMatch.UpdateBlockStatus failed", "start block number", startBlockNumber, "end block number", endBlockNumber, "error", db.Error)
		return fmt.Errorf("MessageMatch.UpdateBlockStatus failed, start block number: %v, end block number: %v, err: %w", startBlockNumber, endBlockNumber, db.Error)
	}
	return nil
}

// UpdateCrossChainStatus updates the cross chain status for the message matches with the provided ids.
func (m *MessageMatch) UpdateCrossChainStatus(ctx context.Context, id []int64, layerType types.LayerType, status types.CrossChainStatusType) error {
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})
	db = db.Where("id in (?)", id)

	var err error
	switch layerType {
	case types.Layer1:
		err = db.Updates(map[string]interface{}{"l1_cross_chain_status": status, "check_status": types.CheckStatusChecked}).Error
	case types.Layer2:
		err = db.Updates(map[string]interface{}{"l2_cross_chain_status": status, "check_status": types.CheckStatusChecked}).Error
	}

	if err != nil {
		log.Warn("MessageMatch.UpdateCrossChainStatus failed", "error", err)
		return fmt.Errorf("MessageMatch.UpdateCrossChainStatus failed err:%w", err)
	}
	return nil
}

// UpdateETHBalance update the eth balance and eth status
func (m *MessageMatch) UpdateETHBalance(ctx context.Context, layerType types.LayerType, messageMatch MessageMatch) error {
	db := m.db.WithContext(ctx)
	db = db.Model(&MessageMatch{})
	db = db.Where("id = ?", messageMatch.ID)

	var err error
	switch layerType {
	case types.Layer1:
		err = db.Updates(map[string]interface{}{"l1_messenger_eth_balance": messageMatch.L1MessengerETHBalance, "l1_eth_balance_status": messageMatch.L1MessengerETHBalance}).Error
	case types.Layer2:
		err = db.Updates(map[string]interface{}{"l2_messenger_eth_balance": messageMatch.L2MessengerETHBalance, "l2_eth_balance_status": messageMatch.L2MessengerETHBalance}).Error
	}
	return err
}
