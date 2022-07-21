package twap_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/osmosis-labs/osmosis/v10/osmoutils"
	"github.com/osmosis-labs/osmosis/v10/x/gamm/twap"
	"github.com/osmosis-labs/osmosis/v10/x/gamm/twap/types"
)

var zeroDec = sdk.ZeroDec()
var oneDec = sdk.OneDec()
var twoDec = oneDec.Add(oneDec)
var OneSec = sdk.NewDec(1e9)

func newRecord(t time.Time, sp0, accum0, accum1 sdk.Dec) types.TwapRecord {
	return types.TwapRecord{
		Asset0Denom:     defaultUniV2Coins[0].Denom,
		Asset1Denom:     defaultUniV2Coins[1].Denom,
		Time:            t,
		P0LastSpotPrice: sp0,
		P1LastSpotPrice: sdk.OneDec().Quo(sp0),
		// make new copies
		P0ArithmeticTwapAccumulator: accum0.Add(sdk.ZeroDec()),
		P1ArithmeticTwapAccumulator: accum1.Add(sdk.ZeroDec()),
	}
}

// make an expected record for math tests, we adjust other values in the test runner.
func newExpRecord(accum0, accum1 sdk.Dec) types.TwapRecord {
	return types.TwapRecord{
		Asset0Denom: defaultUniV2Coins[0].Denom,
		Asset1Denom: defaultUniV2Coins[1].Denom,
		// make new copies
		P0ArithmeticTwapAccumulator: accum0.Add(sdk.ZeroDec()),
		P1ArithmeticTwapAccumulator: accum1.Add(sdk.ZeroDec()),
	}
}

func TestInterpolateRecord(t *testing.T) {
	tests := map[string]struct {
		record          types.TwapRecord
		interpolateTime time.Time
		expRecord       types.TwapRecord
	}{
		"0accum": {
			record:          newRecord(time.Unix(1, 0), sdk.NewDec(10), zeroDec, zeroDec),
			interpolateTime: time.Unix(2, 0),
			expRecord:       newExpRecord(OneSec.MulInt64(10), OneSec.QuoInt64(10)),
		},
		"small starting accumulators": {
			record:          newRecord(time.Unix(1, 0), sdk.NewDec(10), oneDec, twoDec),
			interpolateTime: time.Unix(2, 0),
			expRecord:       newExpRecord(oneDec.Add(OneSec.MulInt64(10)), twoDec.Add(OneSec.QuoInt64(10))),
		},
		"larger time interval": {
			record:          newRecord(time.Unix(11, 0), sdk.NewDec(10), oneDec, twoDec),
			interpolateTime: time.Unix(55, 0),
			expRecord:       newExpRecord(oneDec.Add(OneSec.MulInt64(44*10)), twoDec.Add(OneSec.MulInt64(44).QuoInt64(10))),
		},
		"same time": {
			record:          newRecord(time.Unix(1, 0), sdk.NewDec(10), oneDec, twoDec),
			interpolateTime: time.Unix(1, 0),
			expRecord:       newExpRecord(oneDec, twoDec),
		},
		// TODO: Overflow tests
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// correct expected record based off copy/paste values
			test.expRecord.Time = test.interpolateTime
			test.expRecord.P0LastSpotPrice = test.record.P0LastSpotPrice
			test.expRecord.P1LastSpotPrice = test.record.P1LastSpotPrice

			gotRecord := twap.InterpolateRecord(test.record, test.interpolateTime)
			require.Equal(t, test.expRecord, gotRecord)
		})
	}
}

func (s *TestSuite) TestUpdateTwap() {
	poolId := s.PrepareUni2PoolWithAssets(defaultUniV2Coins[0], defaultUniV2Coins[1])
	newSp := sdk.OneDec()

	tests := map[string]struct {
		record     types.TwapRecord
		updateTime time.Time
		expRecord  types.TwapRecord
	}{
		"0 accum start": {
			record:     newRecord(time.Unix(1, 0), sdk.NewDec(10), zeroDec, zeroDec),
			updateTime: time.Unix(2, 0),
			expRecord:  newExpRecord(OneSec.MulInt64(10), OneSec.QuoInt64(10)),
		},
	}
	for name, test := range tests {
		s.Run(name, func() {
			// setup common, block time, pool Id, expected spot prices
			s.Ctx = s.Ctx.WithBlockTime(test.updateTime.UTC())
			test.record.PoolId = poolId
			test.expRecord.PoolId = poolId
			test.expRecord.P0LastSpotPrice = newSp
			test.expRecord.P1LastSpotPrice = newSp
			test.expRecord.Height = s.Ctx.BlockHeight()
			test.expRecord.Time = s.Ctx.BlockTime()

			newRecord := s.twapkeeper.UpdateRecord(s.Ctx, test.record)
			s.Require().Equal(test.expRecord, newRecord)
		})
	}
}

func TestComputeArithmeticTwap(t *testing.T) {
	denom0, denom1 := "token/B", "token/A"
	newOneSidedRecord := func(time time.Time, accum sdk.Dec, useP0 bool) types.TwapRecord {
		record := types.TwapRecord{Time: time, Asset0Denom: denom0, Asset1Denom: denom1}
		if useP0 {
			record.P0ArithmeticTwapAccumulator = accum
		} else {
			record.P1ArithmeticTwapAccumulator = accum
		}
		return record
	}

	type testCase struct {
		startRecord types.TwapRecord
		endRecord   types.TwapRecord
		quoteAsset  string
		expTwap     sdk.Dec
	}

	baseTime := time.Unix(1257894000, 0).UTC()

	testCaseFromDeltas := func(startAccum, accumDiff sdk.Dec, timeDelta time.Duration, expectedTwap sdk.Dec) testCase {
		return testCase{
			newOneSidedRecord(baseTime, startAccum, true),
			newOneSidedRecord(baseTime.Add(timeDelta), startAccum.Add(accumDiff), true),
			denom0,
			expectedTwap,
		}
	}
	plusOneSec := baseTime.Add(time.Second)
	tests := map[string]testCase{
		"basic: spot price = 1 for one second, 0 init accumulator": {
			newOneSidedRecord(baseTime, sdk.ZeroDec(), true),
			newOneSidedRecord(plusOneSec, OneSec, true),
			denom0,
			sdk.OneDec(),
		},
		// "same record: denom0, unset spot price": {
		// 	newOneSidedRecord(baseTime, sdk.ZeroDec(), true),
		// 	newOneSidedRecord(baseTime, sdk.ZeroDec(), true),
		// 	denom0,
		// 	nil,
		// },
		"accumulator = 10*OneSec, t=5s. 0 base accum":   testCaseFromDeltas(sdk.ZeroDec(), OneSec.MulInt64(10), 5*time.Second, sdk.NewDec(2)),
		"accumulator = 10*OneSec, t=3s. 0 base accum":   testCaseFromDeltas(sdk.ZeroDec(), OneSec.MulInt64(10), 3*time.Second, osmoutils.OneThird),
		"accumulator = 10*OneSec, t=100s. 0 base accum": testCaseFromDeltas(sdk.ZeroDec(), OneSec.MulInt64(10), 100*time.Second, sdk.NewDecWithPrec(1, 1)),
		// TODO: Overflow, rounding, same record tests
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actualTwap := twap.ComputeArithmeticTwap(test.startRecord, test.endRecord, test.quoteAsset)
			require.Equal(t, test.expTwap, actualTwap)
		})
	}
}