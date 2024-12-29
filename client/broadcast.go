package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tendermint/tendermint/mempool"
	tmtypes "github.com/tendermint/tendermint/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx"
)

// BroadcastTx broadcasts a transactions either synchronously or asynchronously
// based on the context parameters. The result of the broadcast is parsed into
// an intermediate structure which is logged if the context has a logger
// defined.
func (ctx Context) BroadcastTx(txBytes []byte) (res *sdk.TxResponse, err error) {
	switch ctx.BroadcastMode {
	case flags.BroadcastSync:
		res, err = ctx.BroadcastTxSync(txBytes)

	case flags.BroadcastAsync:
		res, err = ctx.BroadcastTxAsync(txBytes)

	case flags.BroadcastBlock:
		res, err = ctx.BroadcastTxCommit(txBytes)

	default:
		return nil, fmt.Errorf("unsupported return type %s; supported types: sync, async, block", ctx.BroadcastMode)
	}

	return res, err
}

// CheckTendermintError checks if the error returned from BroadcastTx is a
// Tendermint error that is returned before the tx is submitted due to
// precondition checks that failed. If an Tendermint error is detected, this
// function returns the correct code back in TxResponse.
//
// TODO: Avoid brittle string matching in favor of error matching. This requires
// a change to Tendermint's RPCError type to allow retrieval or matching against
// a concrete error type.
func CheckTendermintError(err error, tx tmtypes.Tx) *sdk.TxResponse {
	if err == nil {
		return nil
	}

	errStr := strings.ToLower(err.Error())
	txHash := fmt.Sprintf("%X", tx.Hash())

	switch {
	case strings.Contains(errStr, strings.ToLower(mempool.ErrTxInCache.Error())):
		return &sdk.TxResponse{
			Code:      sdkerrors.ErrTxInMempoolCache.ABCICode(),
			Codespace: sdkerrors.ErrTxInMempoolCache.Codespace(),
			TxHash:    txHash,
		}

	case strings.Contains(errStr, "mempool is full"):
		return &sdk.TxResponse{
			Code:      sdkerrors.ErrMempoolIsFull.ABCICode(),
			Codespace: sdkerrors.ErrMempoolIsFull.Codespace(),
			TxHash:    txHash,
		}

	case strings.Contains(errStr, "tx too large"):
		return &sdk.TxResponse{
			Code:      sdkerrors.ErrTxTooLarge.ABCICode(),
			Codespace: sdkerrors.ErrTxTooLarge.Codespace(),
			TxHash:    txHash,
		}

	default:
		return nil
	}
}

// BroadcastTxCommit broadcasts transaction bytes to a Tendermint node and
// waits for a commit. An error is only returned if there is no RPC node
// connection or if broadcasting fails.
//
// [AGORIC]: This function subscribes to the transaction's inclusion in a block,
// and then use BroadcastTxSync to broadcast the transaction.  This will block
// potentially forever if the transaction is never included in a block.  And so,
// it is up to the caller to ensure that proper timeout/retry logic is
// implemented.
func (ctx Context) BroadcastTxCommit(txBytes []byte) (*sdk.TxResponse, error) {
	node, err := ctx.GetNode()
	if err != nil {
		return nil, err
	}

	// Start the node if it is not connected.
	nodeWasRunning := node.IsRunning()
	if !nodeWasRunning {
		if err = node.Start(); err != nil {
			return nil, err
		}
	}

	// To prevent races, first subscribe to a query for the transaction's
	// inclusion in a block.  Clean up when we are done.
	query := tmtypes.EventQueryTxFor(txBytes)
	defer func() {
		_ = node.Unsubscribe(context.Background(), "txCommitted", query.String())
		if !nodeWasRunning {
			_ = node.Stop()
		}
	}()
	txch, err := node.Subscribe(context.Background(), "txCommitted", query.String())
	if err != nil {
		return nil, err
	}

	// Broadcast the transaction.
	res, err := node.BroadcastTxSync(context.Background(), txBytes)
	if errRes := CheckTendermintError(err, txBytes); errRes != nil {
		return errRes, nil
	}

	// Check for an error in the broadcast.
	newRes := sdk.NewResponseFormatBroadcastTx(res)
	if newRes.Code != 0 {
		return newRes, err
	}

	// Wait for the tx query to be satisfied.
	ed := <-txch

	// Process the event data and return the commit response.
	var commitRes *sdk.TxResponse
	switch evt := ed.Data.(type) {
	case tmtypes.EventDataTx:
		parsedLogs, _ := sdk.ParseABCILogs(evt.Result.Log)
		commitRes = &sdk.TxResponse{
			TxHash:    newRes.TxHash,
			Height:    evt.Height,
			Codespace: evt.Result.Codespace,
			Code:      evt.Result.Code,
			Data:      strings.ToUpper(hex.EncodeToString(evt.Result.Data)),
			RawLog:    evt.Result.Log,
			Logs:      parsedLogs,
			Info:      evt.Result.Info,
			GasWanted: evt.Result.GasWanted,
			GasUsed:   evt.Result.GasUsed,
			Tx:        newRes.Tx,
			Timestamp: newRes.Timestamp,
			Events:    evt.Result.Events,
		}
		if !evt.Result.IsOK() {
			return commitRes, fmt.Errorf("unexpected result code %d", evt.Result.Code)
		}
		return commitRes, nil
	default:
		parsedLogs, _ := sdk.ParseABCILogs(newRes.RawLog)
		sdkErr := sdkerrors.ErrNotSupported
		commitRes := &sdk.TxResponse{
			Code:      sdkErr.ABCICode(),
			Codespace: sdkErr.Codespace(),
			Data:      newRes.Data,
			RawLog:    newRes.RawLog,
			Logs:      parsedLogs,
			TxHash:    newRes.TxHash,
		}
		return commitRes, fmt.Errorf("unsupported event data type %T", ed.Data)
	}
}

// BroadcastTxSync broadcasts transaction bytes to a Tendermint node
// synchronously (i.e. returns after CheckTx execution).
func (ctx Context) BroadcastTxSync(txBytes []byte) (*sdk.TxResponse, error) {
	node, err := ctx.GetNode()
	if err != nil {
		return nil, err
	}

	res, err := node.BroadcastTxSync(context.Background(), txBytes)
	if errRes := CheckTendermintError(err, txBytes); errRes != nil {
		return errRes, nil
	}

	return sdk.NewResponseFormatBroadcastTx(res), err
}

// BroadcastTxAsync broadcasts transaction bytes to a Tendermint node
// asynchronously (i.e. returns immediately).
func (ctx Context) BroadcastTxAsync(txBytes []byte) (*sdk.TxResponse, error) {
	node, err := ctx.GetNode()
	if err != nil {
		return nil, err
	}

	res, err := node.BroadcastTxAsync(context.Background(), txBytes)
	if errRes := CheckTendermintError(err, txBytes); errRes != nil {
		return errRes, nil
	}

	return sdk.NewResponseFormatBroadcastTx(res), err
}

// TxServiceBroadcast is a helper function to broadcast a Tx with the correct gRPC types
// from the tx service. Calls `clientCtx.BroadcastTx` under the hood.
func TxServiceBroadcast(grpcCtx context.Context, clientCtx Context, req *tx.BroadcastTxRequest) (*tx.BroadcastTxResponse, error) {
	if req == nil || req.TxBytes == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid empty tx")
	}

	clientCtx = clientCtx.WithBroadcastMode(normalizeBroadcastMode(req.Mode))
	resp, err := clientCtx.BroadcastTx(req.TxBytes)
	if err != nil {
		return nil, err
	}

	return &tx.BroadcastTxResponse{
		TxResponse: resp,
	}, nil
}

// normalizeBroadcastMode converts a broadcast mode into a normalized string
// to be passed into the clientCtx.
func normalizeBroadcastMode(mode tx.BroadcastMode) string {
	switch mode {
	case tx.BroadcastMode_BROADCAST_MODE_ASYNC:
		return "async"
	case tx.BroadcastMode_BROADCAST_MODE_BLOCK:
		return "block"
	case tx.BroadcastMode_BROADCAST_MODE_SYNC:
		return "sync"
	default:
		return "unspecified"
	}
}
