package twap

import (
	"encoding/binary"
	"errors"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/osmosis-labs/osmosis/v10/osmoutils"
	"github.com/osmosis-labs/osmosis/v10/x/gamm/twap/types"
)

func (k Keeper) trackChangedPool(ctx sdk.Context, poolId uint64) {
	store := ctx.TransientStore(k.transientKey)
	poolIdBz := make([]byte, 8)
	binary.LittleEndian.PutUint64(poolIdBz, poolId)
	// just has to not be empty, for store to work / not register as a delete.
	sentinelExistsValue := []byte{1}
	store.Set(poolIdBz, sentinelExistsValue)
}

func (k Keeper) getChangedPools(ctx sdk.Context) []uint64 {
	store := ctx.TransientStore(k.transientKey)
	iter := store.Iterator(nil, nil)
	defer iter.Close()

	alteredPoolIds := []uint64{}
	for ; iter.Valid(); iter.Next() {
		k := iter.Key()
		poolId := binary.LittleEndian.Uint64(k)
		alteredPoolIds = append(alteredPoolIds, poolId)
	}
	return alteredPoolIds
}

func (k Keeper) storeHistoricalTWAP(ctx sdk.Context, twap types.TwapRecord) {
	store := ctx.KVStore(k.storeKey)
	key1 := types.FormatHistoricalTimeIndexTWAPKey(twap.Time, twap.PoolId, twap.Asset0Denom, twap.Asset1Denom)
	key2 := types.FormatHistoricalPoolIndexTWAPKey(twap.PoolId, twap.Time, twap.Asset0Denom, twap.Asset1Denom)
	osmoutils.MustSet(store, key1, &twap)
	osmoutils.MustSet(store, key2, &twap)
}

func (k Keeper) pruneRecordsBeforeTime(ctx sdk.Context, lastTime time.Time) {
	// TODO: Stub
}

//nolint:unused,deadcode
func (k Keeper) deleteHistoricalRecord(ctx sdk.Context, twap types.TwapRecord) {
	store := ctx.KVStore(k.storeKey)
	key1 := types.FormatHistoricalTimeIndexTWAPKey(twap.Time, twap.PoolId, twap.Asset0Denom, twap.Asset1Denom)
	key2 := types.FormatHistoricalPoolIndexTWAPKey(twap.PoolId, twap.Time, twap.Asset0Denom, twap.Asset1Denom)
	store.Delete(key1)
	store.Delete(key2)
}

func (k Keeper) getMostRecentRecordStoreRepresentation(ctx sdk.Context, poolId uint64, asset0Denom string, asset1Denom string) (types.TwapRecord, error) {
	store := ctx.KVStore(k.storeKey)
	key := types.FormatMostRecentTWAPKey(poolId, asset0Denom, asset1Denom)
	bz := store.Get(key)
	return types.ParseTwapFromBz(bz)
}

func (k Keeper) getAllMostRecentRecordsForPool(ctx sdk.Context, poolId uint64) ([]types.TwapRecord, error) {
	store := ctx.KVStore(k.storeKey)
	return types.GetAllMostRecentTwapsForPool(store, poolId)
}

func (k Keeper) storeNewRecord(ctx sdk.Context, twap types.TwapRecord) {
	store := ctx.KVStore(k.storeKey)
	key := types.FormatMostRecentTWAPKey(twap.PoolId, twap.Asset0Denom, twap.Asset1Denom)
	osmoutils.MustSet(store, key, &twap)
	k.storeHistoricalTWAP(ctx, twap)
}

// returns an error if theres no historical record at or before time.
// (Asking for a time too far back)
func (k Keeper) getRecordAtOrBeforeTime(ctx sdk.Context, poolId uint64, t time.Time, asset0Denom string, asset1Denom string) (types.TwapRecord, error) {
	store := ctx.KVStore(k.storeKey)
	// We make an iteration from time=t + 1ns, to time=0 for this pool.
	// Note that we cannot get any time entries from t + 1ns, as the key would be `prefix|t+1ns`
	// and the end for a reverse iterator is exclusive. Thus the largest key that can be returned
	// begins with a prefix of `prefix|t`
	startKey := types.FormatHistoricalPoolIndexTimePrefix(poolId, time.Unix(0, 0))
	endKey := types.FormatHistoricalPoolIndexTimePrefix(poolId, t.Add(time.Nanosecond))
	lastParsedTime := time.Time{}
	stopFn := func(key []byte) bool {
		// halt iteration if we can't parse the time, or we've successfully parsed
		// a time, and have iterated beyond records for that time.
		parsedTime, err := types.ParseTimeFromHistoricalPoolIndexKey(key)
		if err != nil {
			return true
		}
		if lastParsedTime.After(parsedTime) {
			return true
		}
		lastParsedTime = parsedTime
		return false
	}

	reverseIterate := true
	twaps, err := osmoutils.GetIterValuesWithStop(store, startKey, endKey, reverseIterate, stopFn, types.ParseTwapFromBz)
	if err != nil {
		return types.TwapRecord{}, err
	}
	if len(twaps) == 0 {
		return types.TwapRecord{}, errors.New("looking for a time thats too old, not in the historical index. " +
			" Try storing the accumulator value.")
	}

	for _, twap := range twaps {
		if twap.Asset0Denom == asset0Denom && twap.Asset1Denom == asset1Denom {
			return twap, nil
		}
	}
	return types.TwapRecord{}, errors.New("something went wrong - TWAP not found, but there are twaps available for this time." +
		" Were provided asset0denom and asset1denom correct, and in order (asset0 > asset1)?")
}
