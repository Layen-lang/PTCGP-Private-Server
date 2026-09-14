package playerapi

import (
	"context"
	"errors"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	itemacquisition "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_acquisition"
	shop "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_shop"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type itemShopServer struct {
	api.UnimplementedItemShopServer
	players *player.Manager
}

func (s itemShopServer) GetPurchaseSummariesV1(ctx context.Context, request *api.ItemShopGetPurchaseSummariesV1_Types_Request) (*api.ItemShopGetPurchaseSummariesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	response := &api.ItemShopGetPurchaseSummariesV1_Types_Response{}
	for _, product := range request.GetProductIds() {
		if product == nil {
			continue
		}
		count, loadErr := s.players.ShopPurchaseCount(ctx, current.ID, request.GetShopId(), product.GetProductId())
		if loadErr != nil {
			return nil, status.Error(codes.Internal, "cannot load shop purchases")
		}
		response.PurchaseSummaries = append(response.PurchaseSummaries, shopSummary(request.GetShopId(), product.GetProductId(), count))
	}
	return response, nil
}

func (s itemShopServer) GetPurchaseSummariesV2(ctx context.Context, request *api.ItemShopGetPurchaseSummariesV2_Types_Request) (*api.ItemShopGetPurchaseSummariesV2_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	response := &api.ItemShopGetPurchaseSummariesV2_Types_Response{}
	for _, product := range request.GetProducts() {
		if product == nil {
			continue
		}
		count, loadErr := s.players.ShopPurchaseCount(ctx, current.ID, product.GetShopId(), product.GetProductId())
		if loadErr != nil {
			return nil, status.Error(codes.Internal, "cannot load shop purchases")
		}
		response.PurchaseSummaries = append(response.PurchaseSummaries, shopSummary(product.GetShopId(), product.GetProductId(), count))
	}
	return response, nil
}

func (s itemShopServer) PurchaseV1(ctx context.Context, request *api.ItemShopPurchaseV1_Types_Request) (*api.ItemShopPurchaseV1_Types_Response, error) {
	result, acquisition, err := s.purchase(ctx, request.GetShopId(), request.GetProductId(), request.GetTransactionId(), int64(request.GetAmount()))
	if err != nil {
		return nil, err
	}
	return &api.ItemShopPurchaseV1_Types_Response{ItemAcquisitionResult: acquisition, PurchaseSummary: shopSummary(result.Product.ShopID, result.Product.ID, result.Count)}, nil
}

func (s itemShopServer) ExchangeV1(ctx context.Context, request *api.ItemShopExchangeV1_Types_Request) (*api.ItemShopExchangeV1_Types_Response, error) {
	result, acquisition, err := s.purchase(ctx, request.GetShopId(), request.GetProductId(), request.GetTransactionId(), int64(request.GetAmount()))
	if err != nil {
		return nil, err
	}
	return &api.ItemShopExchangeV1_Types_Response{ItemAcquisitionResult: acquisition, PurchaseSummary: shopSummary(result.Product.ShopID, result.Product.ID, result.Count)}, nil
}

func (s itemShopServer) LiquidateV1(ctx context.Context, request *api.ItemShopLiquidateV1_Types_Request) (*api.ItemShopLiquidateV1_Types_Response, error) {
	result, acquisition, err := s.purchase(ctx, request.GetShopId(), request.GetProductId(), request.GetTransactionId(), int64(request.GetAmount()))
	if err != nil {
		return nil, err
	}
	return &api.ItemShopLiquidateV1_Types_Response{ItemAcquisitionResult: acquisition, PurchaseSummary: shopSummary(result.Product.ShopID, result.Product.ID, result.Count)}, nil
}

func (s itemShopServer) purchase(ctx context.Context, shopID, productID, transactionID string, amount int64) (player.ShopPurchaseResult, *itemacquisition.ItemAcquisitionResult, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return player.ShopPurchaseResult{}, nil, err
	}
	result, err := s.players.PurchaseShopProduct(ctx, current.ID, shopID, productID, transactionID, amount)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) || errors.Is(err, store.ErrRuleViolation) {
			return result, nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return result, nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return result, nil, status.Error(codes.Internal, "cannot load shop inventory")
	}
	rewards := make([]store.ShopInventoryChange, 0, len(result.Rewards))
	if !result.Replay {
		for _, reward := range result.Rewards {
			reward.Amount *= result.Amount
			rewards = append(rewards, reward)
		}
	}
	return result, acquisitionFromChanges(profile, rewards, s.players.Catalog()), nil
}

func shopSummary(shopID, productID string, count int64) *shop.PurchaseSummary {
	return &shop.PurchaseSummary{ShopId: shopID, ProductId: productID, PurchaseAmount: count}
}
