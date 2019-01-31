package app

import (
	"bytes"

	"github.com/tendermint/tendermint/crypto"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"

	"github.com/binance-chain/node/app/pub"
	"github.com/binance-chain/node/app/val"
	"github.com/binance-chain/node/common/fees"
	"github.com/binance-chain/node/common/log"
	"github.com/binance-chain/node/common/types"
)

func distributeFee(ctx sdk.Context, am auth.AccountKeeper, valMapper val.Mapper, publishBlockFee bool) (blockFee pub.BlockFee) {
	fee := fees.Pool.BlockFees()
	defer fees.Pool.Clear()
	blockFee = pub.BlockFee{Height: ctx.BlockHeader().Height}
	if fee.IsEmpty() {
		// no fees in this block
		return
	}

	proposerValAddr := ctx.BlockHeader().ProposerAddress
	proposerAccAddr := getAccAddr(ctx, valMapper, proposerValAddr)
	voteInfos := ctx.VoteInfos()
	valSize := int64(len(voteInfos))
	var validators []string
	if publishBlockFee {
		validators = make([]string, 0, valSize)
		validators = append(validators, string(proposerAccAddr)) // the first validator to publish should be proposer
	}

	if fee.Type == types.FeeForProposer {
		// The proposer's account must be initialized before it becomes a proposer.
		proposerAcc := am.GetAccount(ctx, proposerAccAddr)
		proposerAcc.SetCoins(proposerAcc.GetCoins().Plus(fee.Tokens))
		am.SetAccount(ctx, proposerAcc)
	} else if fee.Type == types.FeeForAll {
		log.Info("Distributing the fees to all the validators",
			"totalFees", fee.Tokens, "validatorSize", valSize)
		avgTokens := sdk.Coins{}
		roundingTokens := sdk.Coins{}
		for _, token := range fee.Tokens {
			// TODO: int64 is enough, will drop big.Int
			// TODO: temporarily, the validators average the fees. Will change to use power as a weight to calc fees.
			amount := token.Amount
			avgAmount := amount / valSize
			roundingAmount := amount - avgAmount*valSize
			if avgAmount != 0 {
				avgTokens = append(avgTokens, sdk.NewCoin(token.Denom, avgAmount))
			}

			if roundingAmount != 0 {
				roundingTokens = append(roundingTokens, sdk.NewCoin(token.Denom, roundingAmount))
			}
		}

		if avgTokens.IsZero() {
			proposerAcc := am.GetAccount(ctx, proposerAccAddr)
			proposerAcc.SetCoins(proposerAcc.GetCoins().Plus(fee.Tokens))
			am.SetAccount(ctx, proposerAcc)
		} else {
			for _, voteInfo := range voteInfos {
				validator := voteInfo.Validator
				accAddr := getAccAddr(ctx, valMapper, validator.Address)
				validatorAcc := am.GetAccount(ctx, accAddr)
				if bytes.Equal(proposerValAddr, validator.Address) {
					if !roundingTokens.IsZero() {
						validatorAcc.SetCoins(validatorAcc.GetCoins().Plus(roundingTokens))
					}
				} else if publishBlockFee {
					validators = append(validators, string(accAddr))
				}
				validatorAcc.SetCoins(validatorAcc.GetCoins().Plus(avgTokens))
				am.SetAccount(ctx, validatorAcc)
			}
		}
	}

	if publishBlockFee {
		blockFee.Fee = fee.String()
		blockFee.Validators = validators
	}

	return
}

func getAccAddr(ctx sdk.Context, mapper val.Mapper, valAddr crypto.Address) sdk.AccAddress {
	accAddr, err := mapper.GetAccAddr(ctx, valAddr)
	if err != nil {
		log.Error("get validator's AccAddress failed", "ValAddr", valAddr)
		panic(err)
	}

	return accAddr
}
