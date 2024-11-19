package vesting

import (
	"context"

	"github.com/hashicorp/go-metrics"

	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/auth/vesting/exported"
	"github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
)

type msgServer struct {
	keeper.AccountKeeper
	types.BankKeeper
	types.StakingKeeper
}

// NewMsgServerImpl returns an implementation of the vesting MsgServer interface,
// wrapping the corresponding keepers.
func NewMsgServerImpl(k keeper.AccountKeeper, bk types.BankKeeper, sk types.StakingKeeper) types.MsgServer {
	return &msgServer{AccountKeeper: k, BankKeeper: bk, StakingKeeper: sk}
}

var _ types.MsgServer = msgServer{}

func (s msgServer) CreateVestingAccount(goCtx context.Context, msg *types.MsgCreateVestingAccount) (*types.MsgCreateVestingAccountResponse, error) {
	from, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.FromAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'from' address: %s", err)
	}

	to, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.ToAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'to' address: %s", err)
	}

	if err := validateAmount(msg.Amount); err != nil {
		return nil, err
	}

	if msg.EndTime <= 0 {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "invalid end time")
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := s.BankKeeper.IsSendEnabledCoins(ctx, msg.Amount...); err != nil {
		return nil, err
	}

	if s.BankKeeper.BlockedAddr(to) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.ToAddress)
	}

	if acc := s.AccountKeeper.GetAccount(ctx, to); acc != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "account %s already exists", msg.ToAddress)
	}

	baseAccount := authtypes.NewBaseAccountWithAddress(to)
	baseAccount = s.AccountKeeper.NewAccount(ctx, baseAccount).(*authtypes.BaseAccount)
	baseVestingAccount, err := types.NewBaseVestingAccount(baseAccount, msg.Amount.Sort(), msg.EndTime)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}

	var vestingAccount sdk.AccountI
	if msg.Delayed {
		vestingAccount = types.NewDelayedVestingAccountRaw(baseVestingAccount)
	} else {
		vestingAccount = types.NewContinuousVestingAccountRaw(baseVestingAccount, ctx.BlockTime().Unix())
	}

	s.AccountKeeper.SetAccount(ctx, vestingAccount)

	defer func() {
		telemetry.IncrCounter(1, "new", "account")

		for _, a := range msg.Amount {
			if a.Amount.IsInt64() {
				telemetry.SetGaugeWithLabels(
					[]string{"tx", "msg", "create_vesting_account"},
					float32(a.Amount.Int64()),
					[]metrics.Label{telemetry.NewLabel("denom", a.Denom)},
				)
			}
		}
	}()

	if err = s.BankKeeper.SendCoins(ctx, from, to, msg.Amount); err != nil {
		return nil, err
	}

	return &types.MsgCreateVestingAccountResponse{}, nil
}

func (s msgServer) CreatePermanentLockedAccount(goCtx context.Context, msg *types.MsgCreatePermanentLockedAccount) (*types.MsgCreatePermanentLockedAccountResponse, error) {
	from, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.FromAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'from' address: %s", err)
	}

	to, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.ToAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'to' address: %s", err)
	}

	if err := validateAmount(msg.Amount); err != nil {
		return nil, err
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := s.BankKeeper.IsSendEnabledCoins(ctx, msg.Amount...); err != nil {
		return nil, err
	}

	if s.BankKeeper.BlockedAddr(to) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.ToAddress)
	}

	if acc := s.AccountKeeper.GetAccount(ctx, to); acc != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "account %s already exists", msg.ToAddress)
	}

	baseAccount := authtypes.NewBaseAccountWithAddress(to)
	baseAccount = s.AccountKeeper.NewAccount(ctx, baseAccount).(*authtypes.BaseAccount)
	vestingAccount, err := types.NewPermanentLockedAccount(baseAccount, msg.Amount)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}

	s.AccountKeeper.SetAccount(ctx, vestingAccount)

	defer func() {
		telemetry.IncrCounter(1, "new", "account")

		for _, a := range msg.Amount {
			if a.Amount.IsInt64() {
				telemetry.SetGaugeWithLabels(
					[]string{"tx", "msg", "create_permanent_locked_account"},
					float32(a.Amount.Int64()),
					[]metrics.Label{telemetry.NewLabel("denom", a.Denom)},
				)
			}
		}
	}()

	if err = s.BankKeeper.SendCoins(ctx, from, to, msg.Amount); err != nil {
		return nil, err
	}

	return &types.MsgCreatePermanentLockedAccountResponse{}, nil
}

// CreateClawbackVestingAccount creates a new ClawbackVestingAccount, or merges a grant into an existing one.
func (s msgServer) CreateClawbackVestingAccount(goCtx context.Context, msg *types.MsgCreateClawbackVestingAccount) (*types.MsgCreateClawbackVestingAccountResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	ak := s.AccountKeeper
	bk := s.BankKeeper

	from, err := sdk.AccAddressFromBech32(msg.FromAddress)
	if err != nil {
		return nil, err
	}
	to, err := sdk.AccAddressFromBech32(msg.ToAddress)
	if err != nil {
		return nil, err
	}

	if bk.BlockedAddr(to) {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.ToAddress)
	}

	vestingCoins := sdk.NewCoins()
	for _, period := range msg.VestingPeriods {
		vestingCoins = vestingCoins.Add(period.Amount...)
	}

	lockupCoins := sdk.NewCoins()
	for _, period := range msg.LockupPeriods {
		lockupCoins = lockupCoins.Add(period.Amount...)
	}

	if !vestingCoins.IsZero() && len(msg.LockupPeriods) == 0 {
		// If lockup absent, default to an instant unlock schedule
		msg.LockupPeriods = []types.Period{
			{Length: 0, Amount: vestingCoins},
		}
		lockupCoins = vestingCoins
	}

	if !lockupCoins.IsZero() && len(msg.VestingPeriods) == 0 {
		// If vesting absent, default to an instant vesting schedule
		msg.VestingPeriods = []types.Period{
			{Length: 0, Amount: lockupCoins},
		}
		vestingCoins = lockupCoins
	}

	// The vesting and lockup schedules must describe the same total amount.
	// IsEqual can panic, so use (a == b) <=> (a <= b && b <= a).
	if !(vestingCoins.IsAllLTE(lockupCoins) && lockupCoins.IsAllLTE(vestingCoins)) {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "lockup and vesting amounts must be equal")
	}

	madeNewAcc := false
	acc := ak.GetAccount(ctx, to)
	var va exported.ClawbackVestingAccountI

	if acc != nil {
		var isClawback bool
		va, isClawback = acc.(exported.ClawbackVestingAccountI)
		switch {
		case !msg.Merge && isClawback:
			return nil, sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account %s already exists; consider using --merge", msg.ToAddress)
		case !msg.Merge && !isClawback:
			return nil, sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account %s already exists", msg.ToAddress)
		case msg.Merge && !isClawback:
			return nil, sdkerrors.Wrapf(sdkerrors.ErrNotSupported, "account %s must be a clawback vesting account", msg.ToAddress)
		}
		grantAction := types.NewClawbackGrantAction(msg.FromAddress, s.StakingKeeper, msg.GetStartTime(), msg.GetLockupPeriods(), msg.GetVestingPeriods(), vestingCoins)
		err := va.AddGrant(ctx, grantAction)
		if err != nil {
			return nil, err
		}
	} else {
		baseAccount := ak.NewAccountWithAddress(ctx, to)
		va = types.NewClawbackVestingAccount(baseAccount.(*authtypes.BaseAccount), from, vestingCoins, msg.StartTime, msg.LockupPeriods, msg.VestingPeriods)
		madeNewAcc = true
	}

	ak.SetAccount(ctx, va)

	if madeNewAcc {
		defer func() {
			telemetry.IncrCounter(1, "new", "account")

			for _, a := range vestingCoins {
				if a.Amount.IsInt64() {
					telemetry.SetGaugeWithLabels(
						[]string{"tx", "msg", "create_clawback_vesting_account"},
						float32(a.Amount.Int64()),
						[]metrics.Label{telemetry.NewLabel("denom", a.Denom)},
					)
				}
			}
		}()
	}

	err = bk.SendCoins(ctx, from, to, vestingCoins)
	if err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			sdk.EventTypeMessage,
			sdk.NewAttribute(sdk.AttributeKeyModule, types.AttributeValueCategory),
		),
	)

	return &types.MsgCreateClawbackVestingAccountResponse{}, nil
}

// Clawback removes the unvested amount from a ClawbackVestingAccount.
// The destination defaults to the funder address, but can be overridden.
func (s msgServer) Clawback(goCtx context.Context, msg *types.MsgClawback) (*types.MsgClawbackResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	ak := s.AccountKeeper
	bk := s.BankKeeper

	funder, err := sdk.AccAddressFromBech32(msg.GetFunderAddress())
	if err != nil {
		return nil, err
	}
	addr, err := sdk.AccAddressFromBech32(msg.GetAddress())
	if err != nil {
		return nil, err
	}
	dest := funder
	if msg.GetDestAddress() != "" {
		dest, err = sdk.AccAddressFromBech32(msg.GetDestAddress())
		if err != nil {
			return nil, err
		}
	}

	if bk.BlockedAddr(dest) {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.DestAddress)
	}

	acc := ak.GetAccount(ctx, addr)
	if acc == nil {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrNotFound, "account %s does not exist", msg.Address)
	}
	va, ok := acc.(exported.ClawbackVestingAccountI)
	if !ok {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account not subject to clawback: %s", msg.Address)
	}

	clawbackAction := types.NewClawbackAction(funder, dest, ak, bk, s.StakingKeeper)
	err = va.Clawback(ctx, clawbackAction)
	if err != nil {
		return nil, err
	}

	return &types.MsgClawbackResponse{}, nil
}

// ReturnGrants removes the unvested amount from a vesting account,
// returning it to the funder. Currently only supported for ClawbackVestingAccount.
func (s msgServer) ReturnGrants(goCtx context.Context, msg *types.MsgReturnGrants) (*types.MsgReturnGrantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	ak := s.AccountKeeper

	addr, err := sdk.AccAddressFromBech32(msg.GetAddress())
	if err != nil {
		return nil, err
	}

	acc := ak.GetAccount(ctx, addr)
	if acc == nil {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrNotFound, "account %s does not exist", msg.Address)
	}
	va, ok := acc.(exported.ClawbackVestingAccountI)
	if !ok {
		return nil, sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account does not support return-grants: %s", msg.Address)
	}

	returnGrantsAction := types.NewReturnGrantAction(ak, s.BankKeeper, s.StakingKeeper)
	err = va.ReturnGrants(ctx, returnGrantsAction)
	if err != nil {
		return nil, err
	}

	return &types.MsgReturnGrantsResponse{}, nil
}

func (s msgServer) CreatePeriodicVestingAccount(goCtx context.Context, msg *types.MsgCreatePeriodicVestingAccount) (*types.MsgCreatePeriodicVestingAccountResponse, error) {
	from, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.FromAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'from' address: %s", err)
	}

	to, err := s.AccountKeeper.AddressCodec().StringToBytes(msg.ToAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid 'to' address: %s", err)
	}

	if msg.StartTime < 1 {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "invalid start time of %d, length must be greater than 0", msg.StartTime)
	}

	if s.BankKeeper.BlockedAddr(to) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.ToAddress)
	}

	var totalCoins sdk.Coins
	for i, period := range msg.VestingPeriods {
		if period.Length < 1 {
			return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "invalid period length of %d in period %d, length must be greater than 0", period.Length, i)
		}

		if err := validateAmount(period.Amount); err != nil {
			return nil, err
		}

		totalCoins = totalCoins.Add(period.Amount...)
	}

	if s.BankKeeper.BlockedAddr(to) {
		return nil, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", msg.ToAddress)
	}

	ctx := sdk.UnwrapSDKContext(goCtx)
	if acc := s.AccountKeeper.GetAccount(ctx, to); acc != nil {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "account %s already exists", msg.ToAddress)
	}

	if err := s.BankKeeper.IsSendEnabledCoins(ctx, totalCoins...); err != nil {
		return nil, err
	}

	baseAccount := authtypes.NewBaseAccountWithAddress(to)
	baseAccount = s.AccountKeeper.NewAccount(ctx, baseAccount).(*authtypes.BaseAccount)
	vestingAccount, err := types.NewPeriodicVestingAccount(baseAccount, totalCoins.Sort(), msg.StartTime, msg.VestingPeriods)
	if err != nil {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}

	s.AccountKeeper.SetAccount(ctx, vestingAccount)

	if madeNewAcc {
		defer func() {
			telemetry.IncrCounter(1, "new", "account")

			for _, a := range totalCoins {
				if a.Amount.IsInt64() {
					telemetry.SetGaugeWithLabels(
						[]string{"tx", "msg", "create_periodic_vesting_account"},
						float32(a.Amount.Int64()),
						[]metrics.Label{telemetry.NewLabel("denom", a.Denom)},
					)
				}
			}
		}()
	}

	if err = s.BankKeeper.SendCoins(ctx, from, to, totalCoins); err != nil {
		return nil, err
	}

	return &types.MsgCreatePeriodicVestingAccountResponse{}, nil
}

func validateAmount(amount sdk.Coins) error {
	if !amount.IsValid() {
		return sdkerrors.ErrInvalidCoins.Wrap(amount.String())
	}

	if !amount.IsAllPositive() {
		return sdkerrors.ErrInvalidCoins.Wrap(amount.String())
	}

	return nil
}
