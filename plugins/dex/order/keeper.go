package order

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/BiJie/BinanceChain/matcheng"

	"github.com/BiJie/BinanceChain/common/types"
	"github.com/BiJie/BinanceChain/common/utils"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/bank"

	me "github.com/BiJie/BinanceChain/matcheng"
)

// in the future, this may be distributed via Sharding
type Keeper struct {
	ck             bank.Keeper
	storeKey       sdk.StoreKey // The key used to access the store from the Context.
	codespace      sdk.CodespaceType
	engines        map[string]*me.MatchEng
	allOrders      map[string]NewOrderMsg
	roundOrders    map[string]int // limit to the total tx number in a block
	roundIOCOrders map[string][]string
	poolSize       uint // number of concurrent channels, counted in the pow of 2
}

// NewKeeper - Returns the Keeper
func NewKeeper(key sdk.StoreKey, bankKeeper bank.Keeper, codespace sdk.CodespaceType, concurrency uint) Keeper {
	return Keeper{ck: bankKeeper, storeKey: key, codespace: codespace,
		engines: make(map[string]*me.MatchEng), allOrders: make(map[string]NewOrderMsg, 1000000),
		roundOrders: make(map[string]int, 256), roundIOCOrders: make(map[string][]string, 256), poolSize: concurrency}
}

func CreateMatchEng(symbol string) *me.MatchEng {
	//TODO: read lot size
	return me.NewMatchEng(1000, 1, 0.05)
}

func (kp *Keeper) AddOrder(msg NewOrderMsg, height int64) (err error) {
	//try update order book first
	symbol := msg.Symbol
	eng, ok := kp.engines[symbol]
	if !ok {
		eng = CreateMatchEng(symbol)
		kp.engines[symbol] = eng
	}
	_, err = eng.Book.InsertOrder(msg.Id, msg.Side, height, msg.Price, msg.Quantity)
	if err != nil {
		kp.allOrders[msg.Id] = msg
		kp.roundOrders[symbol] += 1
		if msg.TimeInForce == TimeInForce.IOC {
			kp.roundIOCOrders[symbol] = append(kp.roundIOCOrders[symbol], msg.Id)
		}
	}
	return err
}

func (kp *Keeper) RemoveOrder(id string, symbol string, side int8, price int64) (ord me.OrderPart, err error) {
	_, ok := kp.allOrders[id]
	if !ok {
		return me.OrderPart{}, errors.New(fmt.Sprintf("Failed to find order [%v] on symbol [%v]", id, symbol))
	}
	eng, ok := kp.engines[symbol]
	if !ok {
		return me.OrderPart{}, errors.New(fmt.Sprintf("Failed to find order [%v] on symbol [%v]", id, symbol))
	}
	delete(kp.allOrders, id)
	return eng.Book.RemoveOrder(id, side, price)
}

func (kp *Keeper) OrderExists(id string) (NewOrderMsg, bool) {
	ord, ok := kp.allOrders[id]
	return ord, ok
}

type transfer struct {
	account sdk.AccAddress
	inCcy   string
	in      int64
	outCcy  string
	out     int64
}

func (kp *Keeper) tradeToTransfers(trade me.Trade, tradeCcy, quoteCcy string) (transfer, transfer) {
	seller := kp.allOrders[trade.SId].Sender
	buyer := kp.allOrders[trade.BId].Sender
	// TODO: where is 10^8 stored?
	quoteQty := trade.LastPx * trade.LastQty / 1e8
	return transfer{seller, quoteCcy, quoteQty, tradeCcy, trade.LastQty},
		transfer{buyer, tradeCcy, trade.LastQty, quoteCcy, quoteQty}
}

//TODO: should get an even hash
func channelHash(account sdk.AccAddress) int {
	return int(account[0] + account[1])
}

func (kp *Keeper) matchAndDistributeTrades(wg *sync.WaitGroup) []chan transfer {
	size := len(kp.roundOrders)
	if size == 0 {
		return nil
	}
	channelSize := size >> kp.poolSize
	concurrency := 1 << kp.poolSize
	outs := make([]chan string, concurrency)
	for i, _ := range outs {
		outs[i] = make(chan string, channelSize+1)
	}
	i, j, t := 0, 0, channelSize
	for k, _ := range kp.roundOrders {
		i++
		if i >= t {
			j++
			t += channelSize
		}
		outs[j] <- k
	}
	tradeOuts := make([]chan transfer, concurrency)
	for i, _ := range tradeOuts {
		tradeOuts[i] = make(chan transfer)
	}
	wg.Add(concurrency)
	for i = 0; i < concurrency; i++ {
		channel := outs[i]
		go func() {
			for ts := range channel {
				engine := kp.engines[ts]
				if engine.Match() {
					tradeCcy, quoteCcy, _ := utils.TradeSymbol2Ccy(ts)
					for _, t := range engine.Trades {
						t1, t2 := kp.tradeToTransfers(t, tradeCcy, quoteCcy)
						//TODO: calculate fees as transfer, f1, f2, and push into the tradeOuts
						c := channelHash(t1.account) % concurrency
						tradeOuts[c] <- t1
						c = channelHash(t2.account) % concurrency
						tradeOuts[c] <- t2
					}
					engine.DropFilledOrder()
				} // TODO: when Match() failed, have to unsolicited cancel all the orders
				// when multiple unsolicited cancel happened, the validator would stop running
				// and ask for help
				iocIDs := kp.roundIOCOrders[ts]
				for _, id := range iocIDs {
					if msg, ok := kp.allOrders[id]; ok {
						if ord, err := kp.RemoveOrder(msg.Id, msg.Symbol, msg.Side, msg.Price); err != nil {
							//here is a trick to use the same currency as in and out ccy to simulate cancel
							qty := ord.LeavesQty()
							c := channelHash(msg.Sender)
							tradeCcy, _, _ := utils.TradeSymbol2Ccy(msg.Symbol)
							tradeOuts[c] <- transfer{msg.Sender, tradeCcy, qty, tradeCcy, qty}
						}
					}
				}
			}
			wg.Done()
		}()
	}
	for _, c := range outs {
		close(c)
	}
	return tradeOuts
}

func (kp *Keeper) GetOrderBookUnSafe(pair string, levelNum int, iterBuy matcheng.LevelIter, iterSell matcheng.LevelIter) {
	if eng, ok := kp.engines[pair]; ok {
		eng.Book.ShowDepth(levelNum, iterBuy, iterSell)
	}
}

func (kp *Keeper) ClearOrderBook(pair string) {
	if eng, ok := kp.engines[pair]; ok {
		eng.Book.Clear()
	}
}

func (kp *Keeper) doTransfer(ctx sdk.Context, accountMapper auth.AccountMapper, tran transfer) sdk.Error {
	//TODO: error handling
	_, _, sdkErr := kp.ck.AddCoins(ctx, tran.account, sdk.Coins{sdk.Coin{Denom: tran.inCcy, Amount: sdk.NewInt(tran.in)}})
	//for Out, only need to reduce the locked.
	account := accountMapper.GetAccount(ctx, tran.account).(types.NamedAccount)
	account.SetLockedCoins(account.GetLockedCoins().Minus(append(sdk.Coins{}, sdk.Coin{Denom: tran.outCcy, Amount: sdk.NewInt(tran.out)})))
	accountMapper.SetAccount(ctx, account)
	return sdkErr
}

func (kp *Keeper) clearAfterMatch() (err error) {
	kp.roundOrders = make(map[string]int, 256)
	kp.roundIOCOrders = make(map[string][]string, 256)
	return nil
}

// MatchAndAllocateAll() is concurrently matching and allocating across
// all the symbols' order books, among all the clients
func (kp *Keeper) MatchAndAllocateAll(ctx sdk.Context, accountMapper auth.AccountMapper) (code sdk.CodeType, err error) {
	var wg sync.WaitGroup
	allocate := func(ctx sdk.Context, accountMapper auth.AccountMapper, c <-chan transfer) {
		for n := range c {
			kp.doTransfer(ctx, accountMapper, n)
		}
		wg.Done()
	}
	var wgOrd sync.WaitGroup
	tradeOuts := kp.matchAndDistributeTrades(&wgOrd)
	if tradeOuts == nil {
		//TODO: logging
		return sdk.CodeOK, nil
	}

	wg.Add(len(tradeOuts))
	for _, c := range tradeOuts {
		go allocate(ctx, accountMapper, c)
	}
	wgOrd.Wait()
	for _, t := range tradeOuts {
		close(t)
	}
	wg.Wait()
	return sdk.CodeOK, nil
}

// Key to knowing the trend on the streets!
var makerFeeKey = []byte("MakerFee")
var takerFeeKey = []byte("TakerFee")
var feeFactorKey = []byte("FeeFactor")
var maxFeeKey = []byte("MaxFee")
var nativeTokenDiscountKey = []byte("NativeTokenDiscount")
var volumeBucketDurationKey = []byte("VolumeBucketDuration")

func itob(num int64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutVarint(buf, num)
	b := buf[:n]
	return b
}

func btoi(bytes []byte) int64 {
	x, _ := binary.Varint(bytes)
	return x
}

// GetFees - returns the current fees settings
func (k Keeper) GetFees(ctx sdk.Context) (
	makerFee int64, takerFee int64, feeFactor int64, maxFee int64, nativeTokenDiscount int64, volumeBucketDuration int64,
) {
	store := ctx.KVStore(k.storeKey)
	makerFee = btoi(store.Get(makerFeeKey))
	takerFee = btoi(store.Get(takerFeeKey))
	feeFactor = btoi(store.Get(feeFactorKey))
	maxFee = btoi(store.Get(maxFeeKey))
	nativeTokenDiscount = btoi(store.Get(nativeTokenDiscountKey))
	volumeBucketDuration = btoi(store.Get(volumeBucketDurationKey))
	return makerFee, takerFee, feeFactor, maxFee, nativeTokenDiscount, volumeBucketDuration
}

func (k Keeper) setMakerFee(ctx sdk.Context, makerFee int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(makerFee)
	store.Set(makerFeeKey, b)
}

func (k Keeper) setTakerFee(ctx sdk.Context, takerFee int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(takerFee)
	store.Set(takerFeeKey, b)
}

func (k Keeper) setFeeFactor(ctx sdk.Context, feeFactor int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(feeFactor)
	store.Set(feeFactorKey, b)
}

func (k Keeper) setMaxFee(ctx sdk.Context, maxFee int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(maxFee)
	store.Set(maxFeeKey, b)
}

func (k Keeper) setNativeTokenDiscount(ctx sdk.Context, nativeTokenDiscount int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(nativeTokenDiscount)
	store.Set(nativeTokenDiscountKey, b)
}

func (k Keeper) setVolumeBucketDuration(ctx sdk.Context, volumeBucketDuration int64) {
	store := ctx.KVStore(k.storeKey)
	b := itob(volumeBucketDuration)
	store.Set(volumeBucketDurationKey, b)
}

// InitGenesis - store the genesis trend
func (k Keeper) InitGenesis(ctx sdk.Context, data TradingGenesis) error {
	k.setMakerFee(ctx, data.MakerFee)
	k.setTakerFee(ctx, data.TakerFee)
	k.setFeeFactor(ctx, data.FeeFactor)
	k.setMaxFee(ctx, data.MaxFee)
	k.setNativeTokenDiscount(ctx, data.NativeTokenDiscount)
	k.setVolumeBucketDuration(ctx, data.VolumeBucketDuration)
	return nil
}