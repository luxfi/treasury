package settlement

import (
	"context"
	"fmt"
	"math/big"

	"github.com/luxfi/treasury/pkg/ledger"
)

const (
	ledgerName     = "trades"
	escrowAddress  = "escrow:trades"
	feesAddress    = "platform:fees"
	assetUSD       = "USD/2"
)

// Service settles secondary market trades via the double-entry ledger.
type Service struct {
	ledger *ledger.Service
}

// NewService creates a trade settlement service backed by the ledger.
func NewService(l *ledger.Service) *Service {
	return &Service{ledger: l}
}

// SettleTrade posts an atomic multi-posting transaction to settle a trade.
// The flow is:
//  1. Buyer wallet -> Escrow (gross amount)
//  2. Escrow -> Seller wallet (net of commissions and platform fee)
//  3. Escrow -> Buyer BD commissions account
//  4. Escrow -> Seller BD commissions account
//  5. Escrow -> Platform fees (if any)
//
// Uses reference "settle:{tradeID}" for idempotency.
func (s *Service) SettleTrade(ctx context.Context, req TradeSettlementRequest) (*TradeSettlementResult, error) {
	if req.TradeID == "" {
		return nil, fmt.Errorf("trade_id is required")
	}
	if req.BuyerWalletID == "" || req.SellerWalletID == "" {
		return nil, fmt.Errorf("buyer and seller wallet IDs are required")
	}

	grossAmt, ok := new(big.Int).SetString(req.GrossAmount, 10)
	if !ok || grossAmt.Sign() <= 0 {
		return nil, fmt.Errorf("gross_amount must be a positive integer, got %q", req.GrossAmount)
	}
	buyerComm, ok := new(big.Int).SetString(req.BuyerCommission, 10)
	if !ok || buyerComm.Sign() < 0 {
		return nil, fmt.Errorf("buyer_commission must be a non-negative integer, got %q", req.BuyerCommission)
	}
	sellerComm, ok := new(big.Int).SetString(req.SellerCommission, 10)
	if !ok || sellerComm.Sign() < 0 {
		return nil, fmt.Errorf("seller_commission must be a non-negative integer, got %q", req.SellerCommission)
	}
	platformFee, ok := new(big.Int).SetString(req.PlatformFee, 10)
	if !ok || platformFee.Sign() < 0 {
		return nil, fmt.Errorf("platform_fee must be a non-negative integer, got %q", req.PlatformFee)
	}

	// Net to seller = gross - seller commission - platform fee
	netToSeller := new(big.Int).Sub(grossAmt, sellerComm)
	netToSeller.Sub(netToSeller, platformFee)
	if netToSeller.Sign() <= 0 {
		return nil, fmt.Errorf("net amount to seller is non-positive: commissions and fees exceed gross amount")
	}

	// The total buyer pays = gross + buyer commission
	totalBuyerPays := new(big.Int).Add(grossAmt, buyerComm)

	asset := assetUSD
	if req.Currency != "" {
		asset = req.Currency + "/2"
	}

	buyerBDAddr := fmt.Sprintf("commissions:buyer_bd:%s", req.TradeID)
	sellerBDAddr := fmt.Sprintf("commissions:seller_bd:%s", req.TradeID)

	var postings []ledger.Posting

	// 1. Buyer -> Escrow (total buyer pays = gross + buyer commission)
	postings = append(postings, ledger.Posting{
		Source:      req.BuyerWalletID,
		Destination: escrowAddress,
		Asset:       asset,
		Amount:      totalBuyerPays.String(),
	})

	// 2. Escrow -> Seller (net amount)
	postings = append(postings, ledger.Posting{
		Source:      escrowAddress,
		Destination: req.SellerWalletID,
		Asset:       asset,
		Amount:      netToSeller.String(),
	})

	// 3. Escrow -> Buyer BD commission
	if buyerComm.Sign() > 0 {
		postings = append(postings, ledger.Posting{
			Source:      escrowAddress,
			Destination: buyerBDAddr,
			Asset:       asset,
			Amount:      buyerComm.String(),
		})
	}

	// 4. Escrow -> Seller BD commission
	if sellerComm.Sign() > 0 {
		postings = append(postings, ledger.Posting{
			Source:      escrowAddress,
			Destination: sellerBDAddr,
			Asset:       asset,
			Amount:      sellerComm.String(),
		})
	}

	// 5. Escrow -> Platform fees
	if platformFee.Sign() > 0 {
		postings = append(postings, ledger.Posting{
			Source:      escrowAddress,
			Destination: feesAddress,
			Asset:       asset,
			Amount:      platformFee.String(),
		})
	}

	ref := fmt.Sprintf("settle:%s", req.TradeID)
	tx, err := s.ledger.PostTransaction(ctx, ledgerName, ledger.CreateTransactionRequest{
		Reference: ref,
		Postings:  postings,
		Metadata: map[string]string{
			"trade_id":        req.TradeID,
			"settlement_type": req.SettlementType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("post settlement transaction: %w", err)
	}

	return &TradeSettlementResult{
		TradeID:               req.TradeID,
		LedgerTransactionID:   tx.ID,
		BuyerDebit:            totalBuyerPays.String(),
		SellerCredit:          netToSeller.String(),
		BuyerCommissionDebit:  buyerComm.String(),
		SellerCommissionDebit: sellerComm.String(),
		PlatformFeeDebit:      platformFee.String(),
		Status:                "settled",
	}, nil
}
