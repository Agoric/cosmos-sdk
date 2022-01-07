package types

import (
	"errors"
	"math"
	"time"

	yaml "gopkg.in/yaml.v2"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestexported "github.com/cosmos/cosmos-sdk/x/auth/vesting/exported"
)

// Compile-time type assertions
var (
	_ authtypes.AccountI          = (*BaseVestingAccount)(nil)
	_ vestexported.VestingAccount = (*ContinuousVestingAccount)(nil)
	_ vestexported.VestingAccount = (*PeriodicVestingAccount)(nil)
	_ vestexported.VestingAccount = (*DelayedVestingAccount)(nil)
)

// Base Vesting Account

// NewBaseVestingAccount creates a new BaseVestingAccount object. It is the
// callers responsibility to ensure the base account has sufficient funds with
// regards to the original vesting amount.
func NewBaseVestingAccount(baseAccount *authtypes.BaseAccount, originalVesting sdk.Coins, endTime int64) *BaseVestingAccount {
	return &BaseVestingAccount{
		BaseAccount:      baseAccount,
		OriginalVesting:  originalVesting,
		DelegatedFree:    sdk.NewCoins(),
		DelegatedVesting: sdk.NewCoins(),
		EndTime:          endTime,
	}
}

// LockedCoinsFromVesting returns all the coins that are not spendable (i.e. locked)
// for a vesting account given the current vesting coins. If no coins are locked,
// an empty slice of Coins is returned.
//
// CONTRACT: Delegated vesting coins and vestingCoins must be sorted.
func (bva BaseVestingAccount) LockedCoinsFromVesting(vestingCoins sdk.Coins) sdk.Coins {
	lockedCoins := sdk.NewCoins()

	for _, vestingCoin := range vestingCoins {
		vestingAmt := vestingCoin.Amount
		delVestingAmt := bva.DelegatedVesting.AmountOf(vestingCoin.Denom)

		max := sdk.MaxInt(vestingAmt.Sub(delVestingAmt), sdk.ZeroInt())
		lockedCoin := sdk.NewCoin(vestingCoin.Denom, max)

		if !lockedCoin.IsZero() {
			lockedCoins = lockedCoins.Add(lockedCoin)
		}
	}

	return lockedCoins
}

// TrackDelegation tracks a delegation amount for any given vesting account type
// given the amount of coins currently vesting and the current account balance
// of the delegation denominations.
//
// CONTRACT: The account's coins, delegation coins, vesting coins, and delegated
// vesting coins must be sorted.
func (bva *BaseVestingAccount) TrackDelegation(balance, vestingCoins, amount sdk.Coins) {
	for _, coin := range amount {
		baseAmt := balance.AmountOf(coin.Denom)
		vestingAmt := vestingCoins.AmountOf(coin.Denom)
		delVestingAmt := bva.DelegatedVesting.AmountOf(coin.Denom)

		// Panic if the delegation amount is zero or if the base coins does not
		// exceed the desired delegation amount.
		if coin.Amount.IsZero() || baseAmt.LT(coin.Amount) {
			panic("delegation attempt with zero coins or insufficient funds")
		}

		// compute x and y per the specification, where:
		// X := min(max(V - DV, 0), D)
		// Y := D - X
		x := sdk.MinInt(sdk.MaxInt(vestingAmt.Sub(delVestingAmt), sdk.ZeroInt()), coin.Amount)
		y := coin.Amount.Sub(x)

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			bva.DelegatedVesting = bva.DelegatedVesting.Add(xCoin)
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			bva.DelegatedFree = bva.DelegatedFree.Add(yCoin)
		}
	}
}

// TrackUndelegation tracks an undelegation amount by setting the necessary
// values by which delegated vesting and delegated vesting need to decrease and
// by which amount the base coins need to increase.
//
// NOTE: The undelegation (bond refund) amount may exceed the delegated
// vesting (bond) amount due to the way undelegation truncates the bond refund,
// which can increase the validator's exchange rate (tokens/shares) slightly if
// the undelegated tokens are non-integral.
//
// CONTRACT: The account's coins and undelegation coins must be sorted.
func (bva *BaseVestingAccount) TrackUndelegation(amount sdk.Coins) {
	for _, coin := range amount {
		// panic if the undelegation amount is zero
		if coin.Amount.IsZero() {
			panic("undelegation attempt with zero coins")
		}
		delegatedFree := bva.DelegatedFree.AmountOf(coin.Denom)
		delegatedVesting := bva.DelegatedVesting.AmountOf(coin.Denom)

		// compute x and y per the specification, where:
		// X := min(DF, D)
		// Y := min(DV, D - X)
		x := sdk.MinInt(delegatedFree, coin.Amount)
		y := sdk.MinInt(delegatedVesting, coin.Amount.Sub(x))

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			bva.DelegatedFree = bva.DelegatedFree.Sub(sdk.Coins{xCoin})
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			bva.DelegatedVesting = bva.DelegatedVesting.Sub(sdk.Coins{yCoin})
		}
	}
}

// GetOriginalVesting returns a vesting account's original vesting amount
func (bva BaseVestingAccount) GetOriginalVesting() sdk.Coins {
	return bva.OriginalVesting
}

// GetDelegatedFree returns a vesting account's delegation amount that is not
// vesting.
func (bva BaseVestingAccount) GetDelegatedFree() sdk.Coins {
	return bva.DelegatedFree
}

// GetDelegatedVesting returns a vesting account's delegation amount that is
// still vesting.
func (bva BaseVestingAccount) GetDelegatedVesting() sdk.Coins {
	return bva.DelegatedVesting
}

// GetEndTime returns a vesting account's end time
func (bva BaseVestingAccount) GetEndTime() int64 {
	return bva.EndTime
}

// Validate checks for errors on the account fields
func (bva BaseVestingAccount) Validate() error {
	if !(bva.DelegatedVesting.IsAllLTE(bva.OriginalVesting)) {
		return errors.New("delegated vesting amount cannot be greater than original vesting amount")
	}
	return bva.BaseAccount.Validate()
}

type vestingAccountYAML struct {
	Address          sdk.AccAddress `json:"address" yaml:"address"`
	PubKey           string         `json:"public_key" yaml:"public_key"`
	AccountNumber    uint64         `json:"account_number" yaml:"account_number"`
	Sequence         uint64         `json:"sequence" yaml:"sequence"`
	OriginalVesting  sdk.Coins      `json:"original_vesting" yaml:"original_vesting"`
	DelegatedFree    sdk.Coins      `json:"delegated_free" yaml:"delegated_free"`
	DelegatedVesting sdk.Coins      `json:"delegated_vesting" yaml:"delegated_vesting"`
	EndTime          int64          `json:"end_time" yaml:"end_time"`

	// custom fields based on concrete vesting type which can be omitted
	StartTime      int64   `json:"start_time,omitempty" yaml:"start_time,omitempty"`
	VestingPeriods Periods `json:"vesting_periods,omitempty" yaml:"vesting_periods,omitempty"`
}

func (bva BaseVestingAccount) String() string {
	out, _ := bva.MarshalYAML()
	return out.(string)
}

// MarshalYAML returns the YAML representation of a BaseVestingAccount.
func (bva BaseVestingAccount) MarshalYAML() (interface{}, error) {
	accAddr, err := sdk.AccAddressFromBech32(bva.Address)
	if err != nil {
		return nil, err
	}

	out := vestingAccountYAML{
		Address:          accAddr,
		AccountNumber:    bva.AccountNumber,
		PubKey:           getPKString(bva),
		Sequence:         bva.Sequence,
		OriginalVesting:  bva.OriginalVesting,
		DelegatedFree:    bva.DelegatedFree,
		DelegatedVesting: bva.DelegatedVesting,
		EndTime:          bva.EndTime,
	}
	return marshalYaml(out)
}

// Continuous Vesting Account

var _ vestexported.VestingAccount = (*ContinuousVestingAccount)(nil)
var _ authtypes.GenesisAccount = (*ContinuousVestingAccount)(nil)

// NewContinuousVestingAccountRaw creates a new ContinuousVestingAccount object from BaseVestingAccount
func NewContinuousVestingAccountRaw(bva *BaseVestingAccount, startTime int64) *ContinuousVestingAccount {
	return &ContinuousVestingAccount{
		BaseVestingAccount: bva,
		StartTime:          startTime,
	}
}

// NewContinuousVestingAccount returns a new ContinuousVestingAccount
func NewContinuousVestingAccount(baseAcc *authtypes.BaseAccount, originalVesting sdk.Coins, startTime, endTime int64) *ContinuousVestingAccount {
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: originalVesting,
		EndTime:         endTime,
	}

	return &ContinuousVestingAccount{
		StartTime:          startTime,
		BaseVestingAccount: baseVestingAcc,
	}
}

// GetVestedCoins returns the total number of vested coins. If no coins are vested,
// nil is returned.
func (cva ContinuousVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	var vestedCoins sdk.Coins

	// We must handle the case where the start time for a vesting account has
	// been set into the future or when the start of the chain is not exactly
	// known.
	if blockTime.Unix() <= cva.StartTime {
		return vestedCoins
	} else if blockTime.Unix() >= cva.EndTime {
		return cva.OriginalVesting
	}

	// calculate the vesting scalar
	x := blockTime.Unix() - cva.StartTime
	y := cva.EndTime - cva.StartTime
	s := sdk.NewDec(x).Quo(sdk.NewDec(y))

	for _, ovc := range cva.OriginalVesting {
		vestedAmt := ovc.Amount.ToDec().Mul(s).RoundInt()
		vestedCoins = append(vestedCoins, sdk.NewCoin(ovc.Denom, vestedAmt))
	}

	return vestedCoins
}

// GetVestingCoins returns the total number of vesting coins. If no coins are
// vesting, nil is returned.
func (cva ContinuousVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return cva.OriginalVesting.Sub(cva.GetVestedCoins(blockTime))
}

// LockedCoins returns the set of coins that are not spendable (i.e. locked),
// defined as the vesting coins that are not delegated.
func (cva ContinuousVestingAccount) LockedCoins(ctx sdk.Context) sdk.Coins {
	return cva.BaseVestingAccount.LockedCoinsFromVesting(cva.GetVestingCoins(ctx.BlockTime()))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (cva *ContinuousVestingAccount) TrackDelegation(blockTime time.Time, balance, amount sdk.Coins) {
	cva.BaseVestingAccount.TrackDelegation(balance, cva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns the time when vesting starts for a continuous vesting
// account.
func (cva ContinuousVestingAccount) GetStartTime() int64 {
	return cva.StartTime
}

// Validate checks for errors on the account fields
func (cva ContinuousVestingAccount) Validate() error {
	if cva.GetStartTime() >= cva.GetEndTime() {
		return errors.New("vesting start-time cannot be before end-time")
	}

	return cva.BaseVestingAccount.Validate()
}

func (cva ContinuousVestingAccount) String() string {
	out, _ := cva.MarshalYAML()
	return out.(string)
}

// MarshalYAML returns the YAML representation of a ContinuousVestingAccount.
func (cva ContinuousVestingAccount) MarshalYAML() (interface{}, error) {
	accAddr, err := sdk.AccAddressFromBech32(cva.Address)
	if err != nil {
		return nil, err
	}

	out := vestingAccountYAML{
		Address:          accAddr,
		AccountNumber:    cva.AccountNumber,
		PubKey:           getPKString(cva),
		Sequence:         cva.Sequence,
		OriginalVesting:  cva.OriginalVesting,
		DelegatedFree:    cva.DelegatedFree,
		DelegatedVesting: cva.DelegatedVesting,
		EndTime:          cva.EndTime,
		StartTime:        cva.StartTime,
	}
	return marshalYaml(out)
}

// Periodic Vesting Account

var _ vestexported.VestingAccount = (*PeriodicVestingAccount)(nil)
var _ authtypes.GenesisAccount = (*PeriodicVestingAccount)(nil)

// NewPeriodicVestingAccountRaw creates a new PeriodicVestingAccount object from BaseVestingAccount
func NewPeriodicVestingAccountRaw(bva *BaseVestingAccount, startTime int64, periods Periods) *PeriodicVestingAccount {
	return &PeriodicVestingAccount{
		BaseVestingAccount: bva,
		StartTime:          startTime,
		VestingPeriods:     periods,
	}
}

// NewPeriodicVestingAccount returns a new PeriodicVestingAccount
func NewPeriodicVestingAccount(baseAcc *authtypes.BaseAccount, originalVesting sdk.Coins, startTime int64, periods Periods) *PeriodicVestingAccount {
	endTime := startTime
	for _, p := range periods {
		endTime += p.Length
	}
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: originalVesting,
		EndTime:         endTime,
	}

	return &PeriodicVestingAccount{
		BaseVestingAccount: baseVestingAcc,
		StartTime:          startTime,
		VestingPeriods:     periods,
	}
}

// GetVestedCoins returns the total number of vested coins. If no coins are vested,
// nil is returned.
func (pva PeriodicVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	coins := ReadSchedule(pva.StartTime, pva.EndTime, pva.VestingPeriods, pva.OriginalVesting, blockTime.Unix())
	if coins.IsZero() {
		return nil
	}
	return coins
}

// GetVestingCoins returns the total number of vesting coins. If no coins are
// vesting, nil is returned.
func (pva PeriodicVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return pva.OriginalVesting.Sub(pva.GetVestedCoins(blockTime))
}

// LockedCoins returns the set of coins that are not spendable (i.e. locked),
// defined as the vesting coins that are not delegated.
func (pva PeriodicVestingAccount) LockedCoins(ctx sdk.Context) sdk.Coins {
	return pva.BaseVestingAccount.LockedCoinsFromVesting(pva.GetVestingCoins(ctx.BlockTime()))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (pva *PeriodicVestingAccount) TrackDelegation(blockTime time.Time, balance, amount sdk.Coins) {
	pva.BaseVestingAccount.TrackDelegation(balance, pva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns the time when vesting starts for a periodic vesting
// account.
func (pva PeriodicVestingAccount) GetStartTime() int64 {
	return pva.StartTime
}

// GetVestingPeriods returns vesting periods associated with periodic vesting account.
func (pva PeriodicVestingAccount) GetVestingPeriods() Periods {
	return pva.VestingPeriods
}

// Validate checks for errors on the account fields
func (pva PeriodicVestingAccount) Validate() error {
	if pva.GetStartTime() >= pva.GetEndTime() {
		return errors.New("vesting start-time cannot be before end-time")
	}
	endTime := pva.StartTime
	originalVesting := sdk.NewCoins()
	for _, p := range pva.VestingPeriods {
		endTime += p.Length
		originalVesting = originalVesting.Add(p.Amount...)
	}
	if endTime != pva.EndTime {
		return errors.New("vesting end time does not match length of all vesting periods")
	}
	if !originalVesting.IsEqual(pva.OriginalVesting) {
		return errors.New("original vesting coins does not match the sum of all coins in vesting periods")
	}

	return pva.BaseVestingAccount.Validate()
}

func (pva PeriodicVestingAccount) String() string {
	out, _ := pva.MarshalYAML()
	return out.(string)
}

// MarshalYAML returns the YAML representation of a PeriodicVestingAccount.
func (pva PeriodicVestingAccount) MarshalYAML() (interface{}, error) {
	accAddr, err := sdk.AccAddressFromBech32(pva.Address)
	if err != nil {
		return nil, err
	}

	out := vestingAccountYAML{
		Address:          accAddr,
		AccountNumber:    pva.AccountNumber,
		PubKey:           getPKString(pva),
		Sequence:         pva.Sequence,
		OriginalVesting:  pva.OriginalVesting,
		DelegatedFree:    pva.DelegatedFree,
		DelegatedVesting: pva.DelegatedVesting,
		EndTime:          pva.EndTime,
		StartTime:        pva.StartTime,
		VestingPeriods:   pva.VestingPeriods,
	}
	return marshalYaml(out)
}

// Delayed Vesting Account

var _ vestexported.VestingAccount = (*DelayedVestingAccount)(nil)
var _ authtypes.GenesisAccount = (*DelayedVestingAccount)(nil)

// NewDelayedVestingAccountRaw creates a new DelayedVestingAccount object from BaseVestingAccount
func NewDelayedVestingAccountRaw(bva *BaseVestingAccount) *DelayedVestingAccount {
	return &DelayedVestingAccount{
		BaseVestingAccount: bva,
	}
}

// NewDelayedVestingAccount returns a DelayedVestingAccount
func NewDelayedVestingAccount(baseAcc *authtypes.BaseAccount, originalVesting sdk.Coins, endTime int64) *DelayedVestingAccount {
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: originalVesting,
		EndTime:         endTime,
	}

	return &DelayedVestingAccount{baseVestingAcc}
}

// GetVestedCoins returns the total amount of vested coins for a delayed vesting
// account. All coins are only vested once the schedule has elapsed.
func (dva DelayedVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	if blockTime.Unix() >= dva.EndTime {
		return dva.OriginalVesting
	}

	return nil
}

// GetVestingCoins returns the total number of vesting coins for a delayed
// vesting account.
func (dva DelayedVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return dva.OriginalVesting.Sub(dva.GetVestedCoins(blockTime))
}

// LockedCoins returns the set of coins that are not spendable (i.e. locked),
// defined as the vesting coins that are not delegated.
func (dva DelayedVestingAccount) LockedCoins(ctx sdk.Context) sdk.Coins {
	return dva.BaseVestingAccount.LockedCoinsFromVesting(dva.GetVestingCoins(ctx.BlockTime()))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (dva *DelayedVestingAccount) TrackDelegation(blockTime time.Time, balance, amount sdk.Coins) {
	dva.BaseVestingAccount.TrackDelegation(balance, dva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns zero since a delayed vesting account has no start time.
func (dva DelayedVestingAccount) GetStartTime() int64 {
	return 0
}

// Validate checks for errors on the account fields
func (dva DelayedVestingAccount) Validate() error {
	return dva.BaseVestingAccount.Validate()
}

func (dva DelayedVestingAccount) String() string {
	out, _ := dva.MarshalYAML()
	return out.(string)
}

//-----------------------------------------------------------------------------
// Permanent Locked Vesting Account

var _ vestexported.VestingAccount = (*PermanentLockedAccount)(nil)
var _ authtypes.GenesisAccount = (*PermanentLockedAccount)(nil)

// NewPermanentLockedAccount returns a PermanentLockedAccount
func NewPermanentLockedAccount(baseAcc *authtypes.BaseAccount, coins sdk.Coins) *PermanentLockedAccount {
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: coins,
		EndTime:         0, // ensure EndTime is set to 0, as PermanentLockedAccount's do not have an EndTime
	}

	return &PermanentLockedAccount{baseVestingAcc}
}

// GetVestedCoins returns the total amount of vested coins for a permanent locked vesting
// account. All coins are only vested once the schedule has elapsed.
func (plva PermanentLockedAccount) GetVestedCoins(_ time.Time) sdk.Coins {
	return nil
}

// GetVestingCoins returns the total number of vesting coins for a permanent locked
// vesting account.
func (plva PermanentLockedAccount) GetVestingCoins(_ time.Time) sdk.Coins {
	return plva.OriginalVesting
}

// LockedCoins returns the set of coins that are not spendable (i.e. locked),
// defined as the vesting coins that are not delegated.
func (plva PermanentLockedAccount) LockedCoins(_ sdk.Context) sdk.Coins {
	return plva.BaseVestingAccount.LockedCoinsFromVesting(plva.OriginalVesting)
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (plva *PermanentLockedAccount) TrackDelegation(blockTime time.Time, balance, amount sdk.Coins) {
	plva.BaseVestingAccount.TrackDelegation(balance, plva.OriginalVesting, amount)
}

// GetStartTime returns zero since a permanent locked vesting account has no start time.
func (plva PermanentLockedAccount) GetStartTime() int64 {
	return 0
}

// GetEndTime returns a vesting account's end time, we return 0 to denote that
// a permanently locked vesting account has no end time.
func (plva PermanentLockedAccount) GetEndTime() int64 {
	return 0
}

// Validate checks for errors on the account fields
func (plva PermanentLockedAccount) Validate() error {
	if plva.EndTime > 0 {
		return errors.New("permanently vested accounts cannot have an end-time")
	}

	return plva.BaseVestingAccount.Validate()
}

func (plva PermanentLockedAccount) String() string {
	out, _ := plva.MarshalYAML()
	return out.(string)
}

type getPK interface {
	GetPubKey() cryptotypes.PubKey
}

func getPKString(g getPK) string {
	if pk := g.GetPubKey(); pk != nil {
		return pk.String()
	}
	return ""
}

func marshalYaml(i interface{}) (interface{}, error) {
	bz, err := yaml.Marshal(i)
	if err != nil {
		return nil, err
	}
	return string(bz), nil
}

// True Vesting Account

var _ vestexported.VestingAccount = (*TrueVestingAccount)(nil)
var _ authtypes.GenesisAccount = (*TrueVestingAccount)(nil)

// NewTrueVestingAccountRaw creates a new TrueVestingAccount object from BaseVestingAccount
func NewTrueVestingAccountRaw(bva *BaseVestingAccount, startTime int64, lockupPeriods, vestingPeriods Periods) *TrueVestingAccount {
	return (&TrueVestingAccount{
		BaseVestingAccount: bva,
		StartTime:          startTime,
		LockupPeriods:      lockupPeriods,
		VestingPeriods:     vestingPeriods,
	}).UpdateCombined()
}

// NewTrueVestingAccount returns a new TrueVestingAccount
func NewTrueVestingAccount(baseAcc *authtypes.BaseAccount, originalVesting sdk.Coins, startTime int64, lockupPeriods, vestingPeriods Periods) *TrueVestingAccount {
	endTime := startTime
	for _, p := range vestingPeriods {
		endTime += p.Length
	}
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: originalVesting,
		EndTime:         endTime,
	}

	return (&TrueVestingAccount{
		BaseVestingAccount: baseVestingAcc,
		StartTime:          startTime,
		LockupPeriods:      lockupPeriods,
		VestingPeriods:     vestingPeriods,
	}).UpdateCombined()
}

func (tva *TrueVestingAccount) UpdateCombined() *TrueVestingAccount {
	// XXX validate that lockup and vesting schedules sum to OriginalVesting
	start, end, combined := ConjunctPeriods(tva.StartTime, tva.StartTime, tva.LockupPeriods, tva.VestingPeriods)
	tva.StartTime = start
	tva.EndTime = end
	tva.CombinedPeriods = combined
	return tva
}

// GetVestedCoins returns the total number of vested coins. If no coins are vested,
// nil is returned.
func (tva TrueVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	// XXX consider not precomputing the combined schedule and just take the
	// min of the lockup and vesting separately. It's likely that one or the
	// other schedule will be nearly trivial, so there should be little overhead
	// in recomputing the conjunction each time.
	coins := ReadSchedule(tva.StartTime, tva.EndTime, tva.CombinedPeriods, tva.OriginalVesting, blockTime.Unix())
	if coins.IsZero() {
		return nil
	}
	return coins
}

// GetVestingCoins returns the total number of vesting coins. If no coins are
// vesting, nil is returned.
func (tva TrueVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return tva.OriginalVesting.Sub(tva.GetVestedCoins(blockTime))
}

// LockedCoins returns the set of coins that are not spendable (i.e. locked),
// defined as the vesting coins that are not delegated.
func (tva TrueVestingAccount) LockedCoins(ctx sdk.Context) sdk.Coins {
	return tva.BaseVestingAccount.LockedCoinsFromVesting(tva.GetVestingCoins(ctx.BlockTime()))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (tva *TrueVestingAccount) TrackDelegation(blockTime time.Time, balance, amount sdk.Coins) {
	tva.BaseVestingAccount.TrackDelegation(balance, tva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns the time when vesting starts for a periodic vesting
// account.
func (tva TrueVestingAccount) GetStartTime() int64 {
	return tva.StartTime
}

// GetVestingPeriods returns vesting periods associated with periodic vesting account.
func (tva TrueVestingAccount) GetVestingPeriods() Periods {
	return tva.VestingPeriods
}

// Validate checks for errors on the account fields
func (tva TrueVestingAccount) Validate() error {
	// XXX ensure that lockup and vesting schedules both sum to OriginalVesting
	if tva.GetStartTime() >= tva.GetEndTime() {
		return errors.New("vesting start-time cannot be before end-time")
	}
	endTime := tva.StartTime
	originalVesting := sdk.NewCoins()
	for _, p := range tva.VestingPeriods {
		endTime += p.Length
		originalVesting = originalVesting.Add(p.Amount...)
	}
	if endTime != tva.EndTime {
		return errors.New("vesting end time does not match length of all vesting periods")
	}
	if !originalVesting.IsEqual(tva.OriginalVesting) {
		return errors.New("original vesting coins does not match the sum of all coins in vesting periods")
	}

	return tva.BaseVestingAccount.Validate()
}

func (pva TrueVestingAccount) String() string {
	out, _ := pva.MarshalYAML()
	return out.(string)
}

// MarshalYAML returns the YAML representation of a TrueVestingAccount.
func (pva TrueVestingAccount) MarshalYAML() (interface{}, error) {
	accAddr, err := sdk.AccAddressFromBech32(pva.Address)
	if err != nil {
		return nil, err
	}

	out := vestingAccountYAML{
		Address:          accAddr,
		AccountNumber:    pva.AccountNumber,
		PubKey:           getPKString(pva),
		Sequence:         pva.Sequence,
		OriginalVesting:  pva.OriginalVesting,
		DelegatedFree:    pva.DelegatedFree,
		DelegatedVesting: pva.DelegatedVesting,
		EndTime:          pva.EndTime,
		StartTime:        pva.StartTime,
		VestingPeriods:   pva.VestingPeriods,
	}
	return marshalYaml(out)
}

// ComputeClawback returns an account with all future vesting events removed,
// plus the total sum of these events. When removing the future vesting events,
// the lockup schedule will also have to be capped to keep the total sums the same.
// (But future unlocking events might be preserved if they unlock currently vested coins.)
// If the amount returned is zero, then the returned account should be unchanged.
// Does not adjust DelegatedVesting
func (tva TrueVestingAccount) ComputeClawback(clawbackTime int64) (TrueVestingAccount, sdk.Coins) {
	// Compute the truncated vesting schedule and amounts.
	// Work with the schedule as the primary data and recompute derived fields, e.g. OriginalVesting.
	t := tva.StartTime
	totalVested := sdk.NewCoins()
	totalUnvested := sdk.NewCoins()
	unvestedIdx := 0
	for i, period := range tva.VestingPeriods {
		t += period.Length
		// tie in time goes to clawback
		if t < clawbackTime {
			totalVested = totalVested.Add(period.Amount...)
			unvestedIdx = i + 1
		} else {
			totalUnvested = totalUnvested.Add(period.Amount...)
		}
	}
	newVestingPeriods := tva.VestingPeriods[:unvestedIdx]

	// To cap the unlocking schedule to the new total vested, conjunct with a limiting schedule
	capPeriods := []Period{
		{
			Length: 0,
			Amount: totalVested,
		},
	}
	_, _, newLockupPeriods := ConjunctPeriods(tva.StartTime, tva.StartTime, tva.LockupPeriods, capPeriods)

	_, _, newCombinedPeriods := ConjunctPeriods(tva.StartTime, tva.StartTime, newLockupPeriods, newVestingPeriods)

	// Now construct the new account state
	tva.OriginalVesting = totalVested
	tva.EndTime = t
	tva.LockupPeriods = newLockupPeriods
	tva.VestingPeriods = newVestingPeriods
	tva.CombinedPeriods = newCombinedPeriods
	// DelegatedVesting will be adjusted elsewhere

	return tva, totalUnvested
}

// Clawback transfers unvested tokens in a TrueVestingAccount to dest.
// Future vesting events are removed. Unstaked tokens
func (tva TrueVestingAccount) Clawback(ctx sdk.Context, dest sdk.AccAddress, ak AccountKeeper, bk BankKeeper, sk StakingKeeper) error {
	updatedAcc, toClawBack := tva.ComputeClawback(ctx.BlockTime().Unix())
	if toClawBack.IsZero() {
		return nil
	}
	addr := updatedAcc.GetAddress()

	accPtr := &updatedAcc
	writeAcc := func() { ak.SetAccount(ctx, accPtr) }
	writeAcc() // do this now so that unvested tokens are unlocked

	// Now that future vesting events (and associated lockup) are removed,
	// the balance of the account is unlocked and can be freely transferred.
	spendable := bk.SpendableCoins(ctx, addr)
	toXfer := coinsMin(toClawBack, spendable)
	err := bk.SendCoins(ctx, addr, dest, toXfer) // unvested tokens are be unlocked now
	if err != nil {
		return err
	}
	toClawBack = toClawBack.Sub(toXfer)
	if toClawBack.IsZero() {
		return nil
	}

	// If we need more, we'll have to transfer unbonding or bonded tokens
	// Staking is the only way unvested tokens should be missing from the bank balance.
	bondDenom := sk.BondDenom(ctx)
	// Safely subtract amt of bondDenom from coins, with a floor of zero.
	subBond := func(coins sdk.Coins, amt sdk.Int) sdk.Coins {
		coinsB := sdk.NewCoins(sdk.NewCoin(bondDenom, amt))
		return coins.Sub(coinsMin(coins, coinsB))
	}
	defer writeAcc() // write again when we're done to update DelegatedVesting

	// If we need more, transfer UnbondingDelegations.
	want := toClawBack.AmountOf(bondDenom)
	unbondings := sk.GetUnbondingDelegations(ctx, addr, math.MaxUint16)
	for _, unbonding := range unbondings {
		transferred := sk.TransferUnbonding(ctx, addr, dest, sdk.ValAddress(unbonding.ValidatorAddress), want)
		updatedAcc.DelegatedVesting = subBond(updatedAcc.DelegatedVesting, transferred)
		want = want.Sub(transferred)
		if !want.IsPositive() {
			return nil
		}
	}

	// If we need more, transfer Delegations.
	delegations := sk.GetDelegatorDelegations(ctx, addr, math.MaxUint16)
	for _, delegation := range delegations {
		validatorAddr, err := sdk.ValAddressFromBech32(delegation.ValidatorAddress)
		if err != nil {
			panic(err) // shouldn't happen
		}
		validator, found := sk.GetValidator(ctx, validatorAddr)
		if !found {
			panic("validator not found") // shoudn't happen
		}
		wantShares, err := validator.SharesFromTokensTruncated(want)
		if err != nil {
			return err // XXX or something
		}
		transferredShares := sk.TransferDelegation(ctx, addr, dest, delegation.GetValidatorAddr(), wantShares)
		transferred := validator.TokensFromSharesRoundUp(transferredShares).RoundInt() // XXX rounding
		updatedAcc.DelegatedVesting = subBond(updatedAcc.DelegatedVesting, transferred)
		want = want.Sub(transferred) // XXX underflow from rounding?
		if !want.IsPositive() {
			return nil
		}
	}

	// If we've transferred everything and still haven't transferred the desired clawback amount,
	// then the account must have most some unvested tokens from slashing.
	return nil
}

// findBalance computes the current account balance on the staking dimension.
// Returns the number of bonded, unbonding, and unbonded statking tokens.
func (tva TrueVestingAccount) findBalance(ctx sdk.Context, bk BankKeeper, sk StakingKeeper) (bonded, unbonding, unbonded sdk.Int) {
	bondDenom := sk.BondDenom(ctx)
	unbonded = bk.GetBalance(ctx, tva.GetAddress(), bondDenom).Amount

	unbonding = sdk.ZeroInt()
	unbondings := sk.GetUnbondingDelegations(ctx, tva.GetAddress(), math.MaxUint16)
	for _, unbonding := range unbondings {
		for _, entry := range unbonding.Entries {
			unbonded = unbonded.Add(entry.Balance)
		}
	}

	bonded = sdk.ZeroInt()
	delegations := sk.GetDelegatorDelegations(ctx, tva.GetAddress(), math.MaxUint16)
	for _, delegation := range delegations {
		validatorAddr, err := sdk.ValAddressFromBech32(delegation.ValidatorAddress)
		if err != nil {
			panic(err) // shouldn't happen
		}
		validator, found := sk.GetValidator(ctx, validatorAddr)
		if !found {
			panic("validator not found") // shoudn't happen
		}
		shares := delegation.Shares
		tokens := validator.TokensFromSharesRoundUp(shares).RoundInt() // XXX rounding
		bonded = bonded.Add(tokens)
	}
	return
}

// distributeReward adds the reward to the future vesting schedule in proportion to the future vesting
// staking tokens.
func (tva TrueVestingAccount) distributeReward(ctx sdk.Context, ak AccountKeeper, bondDenom string, reward sdk.Coins) error {
	now := ctx.BlockTime().Unix()
	t := tva.StartTime
	firstUnvestedPeriod := 0
	unvestedTokens := sdk.ZeroInt()
	for i, period := range tva.VestingPeriods {
		t += period.Length
		if t <= now {
			firstUnvestedPeriod = i + 1
			continue
		}
		unvestedTokens = unvestedTokens.Add(period.Amount.AmountOf(bondDenom))
	}

	runningTotReward := sdk.NewCoins()
	runningTotStaking := sdk.ZeroInt()
	for i := firstUnvestedPeriod; i < len(tva.VestingPeriods); i++ {
		period := tva.VestingPeriods[i]
		runningTotStaking = runningTotStaking.Add(period.Amount.AmountOf(bondDenom))
		runningTotRatio := runningTotStaking.ToDec().Quo(unvestedTokens.ToDec())
		targetCoins := scaleCoins(reward, runningTotRatio)
		thisReward := targetCoins.Sub(runningTotReward)
		runningTotReward = targetCoins
		period.Amount = period.Amount.Add(thisReward...)
		tva.VestingPeriods[i] = period
	}

	tva.OriginalVesting = tva.OriginalVesting.Add(reward...)
	ak.SetAccount(ctx, &tva)
	return nil
}

func scaleCoins(coins sdk.Coins, scale sdk.Dec) sdk.Coins {
	scaledCoins := sdk.NewCoins()
	for _, coin := range coins {
		amt := coin.Amount.ToDec().Mul(scale).RoundInt() // XXX rounding
		scaledCoins = scaledCoins.Add(sdk.NewCoin(coin.Denom, amt))
	}
	return scaledCoins
}

// PostReward encumbers a previously-deposited reward according to the current vesting apportionment of staking.
// Note that rewards might be unvested, but are unlocked.
func (tva TrueVestingAccount) PostReward(ctx sdk.Context, reward sdk.Coins, ak AccountKeeper, bk BankKeeper, sk StakingKeeper) error {
	// Find the scheduled amount of vested and unvested staking tokens
	bondDenom := sk.BondDenom(ctx)
	vested := ReadSchedule(tva.StartTime, tva.EndTime, tva.VestingPeriods, tva.OriginalVesting, ctx.BlockTime().Unix()).AmountOf(bondDenom)
	unvested := tva.OriginalVesting.AmountOf(bondDenom).Sub(vested)

	if unvested.IsZero() {
		// no need to adjust the vesting schedule
		return nil
	}

	if vested.IsZero() {
		// all staked tokens must be unvested
		return tva.distributeReward(ctx, ak, bondDenom, reward)
	}

	// Find current split of account balance on staking axis
	bonded, unbonding, unbonded := tva.findBalance(ctx, bk, sk)
	total := bonded.Add(unbonding).Add(unbonded)

	// Adjust vested/unvested for the actual amount in the account (transfers, slashing)
	if unvested.GT(total) {
		// must have been reduced by slashing
		unvested = total
	}
	vested = total.Sub(unvested)

	// Now restrict to just the bonded tokens, preferring them to be vested
	if vested.GT(bonded) {
		vested = bonded
	}
	unvested = bonded.Sub(vested)

	// Compute the unvested amount of reward and add to vesting schedule
	if unvested.IsZero() {
		return nil
	}
	if vested.IsZero() {
		return tva.distributeReward(ctx, ak, bondDenom, reward)
	}
	unvestedRatio := unvested.ToDec().Quo(bonded.ToDec()) // XXX rounding
	unvestedReward := scaleCoins(reward, unvestedRatio)
	return tva.distributeReward(ctx, ak, bondDenom, unvestedReward)
}
