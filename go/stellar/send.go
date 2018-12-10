package stellar

import (
	"fmt"

	"github.com/keybase/client/go/libkb"
	"github.com/keybase/client/go/protocol/stellar1"
	"github.com/keybase/client/go/stellar/stellarcommon"
)

func SendPaymentLocal(mctx libkb.MetaContext, arg stellar1.SendPaymentLocalArg) (res stellar1.SendPaymentResLocal, err error) {
	if arg.Bid.IsNil() && !arg.BypassBid {
		return res, fmt.Errorf("missing payment ID")
	}

	if len(arg.From) == 0 {
		return res, fmt.Errorf("missing from account ID parameter")
	}

	to := arg.To
	if arg.ToIsAccountID {
		toAccountID, err := libkb.ParseStellarAccountID(arg.To)
		if err != nil {
			if verr, ok := err.(libkb.VerboseError); ok {
				mctx.CDebugf(verr.Verbose())
			}
			return res, fmt.Errorf("recipient: %v", err)
		}
		to = toAccountID.String()
	}

	if !arg.Asset.IsNativeXLM() {
		return res, fmt.Errorf("sending non-XLM assets is not supported")
	}

	var displayBalance DisplayBalance
	if arg.WorthAmount != "" {
		if arg.WorthCurrency == nil {
			return res, fmt.Errorf("missing worth currency")
		}
		displayBalance = DisplayBalance{
			Amount:   arg.WorthAmount,
			Currency: arg.WorthCurrency.String(),
		}
	}

	var data *buildPaymentData
	var release func()
	if !arg.Bid.IsNil() {
		mctx, data, release, err = getGlobal(mctx.G()).acquireBuildPayment(mctx, arg.Bid, arg.SessionID)
		defer release()
		if err != nil {
			return res, err
		}
		if data == nil {
			// Not expected.
			return res, fmt.Errorf("the payment to send was not found")
		}
		mctx.CDebugf("got state readyToReview:%v readyToSend:%v set:%v",
			data.ReadyToReview, data.ReadyToSend, data.Frozen != nil)
		if arg.BypassReview {
			// Pretend that a review occurred and succeeded.
			data.ReadyToSend = data.ReadyToSend || data.ReadyToReview
		}
		err = data.CheckReadyToSend(arg)
		if err != nil {
			return res, err
		}
	}

	sendRes, err := SendPaymentGUI(mctx, getGlobal(mctx.G()).walletState, SendPaymentArg{
		Bid:            arg.Bid,
		From:           arg.From,
		To:             stellarcommon.RecipientInput(to),
		Amount:         arg.Amount,
		DisplayBalance: displayBalance,
		SecretNote:     arg.SecretNote,
		PublicMemo:     arg.PublicMemo,
		ForceRelay:     false,
		QuickReturn:    arg.QuickReturn,
	})
	if err != nil {
		return res, err
	}
	if data != nil {
		data.Stopped = true
	}
	return stellar1.SendPaymentResLocal{
		KbTxID:  sendRes.KbTxID,
		Pending: sendRes.Pending,
	}, nil
}
