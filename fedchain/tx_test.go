package fedchain

import (
	"reflect"
	"testing"
	"time"

	"golang.org/x/net/context"

	"chain/errors"
	"chain/fedchain/bc"
	"chain/fedchain/fedtest"
	"chain/fedchain/memstore"
	"chain/fedchain/txscript"
	"chain/testutil"
)

func TestIdempotentAddTx(t *testing.T) {
	ctx, fc := newContextFC(t)

	issueTx, _, _ := fedtest.Issue(t, nil, nil, 1)

	for i := 0; i < 2; i++ {
		err := fc.AddTx(ctx, issueTx)
		if err != nil {
			testutil.FatalErr(t, err)
		}
	}

	// still idempotent after block lands
	err := fc.AddBlock(ctx, &bc.Block{})
	if err != nil {
		testutil.FatalErr(t, err)
	}
	block, _, err := fc.GenerateBlock(ctx, time.Now())
	block.SignatureScript = []byte{txscript.OP_TRUE}
	err = fc.AddBlock(ctx, block)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	err = fc.AddTx(ctx, issueTx)
	if err != nil {
		testutil.FatalErr(t, err)
	}
}

func TestAddTx(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	fc, err := New(ctx, store, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	issueTx, _, dest1 := fedtest.Issue(t, nil, nil, 1)
	err = fc.AddTx(ctx, issueTx)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	transferTx := fedtest.Transfer(t, fedtest.StateOut(issueTx, 0), dest1, fedtest.Dest(t))
	err = fc.AddTx(ctx, transferTx)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	invalidTransfer := fedtest.Transfer(t, fedtest.StateOut(issueTx, 0), dest1, fedtest.Dest(t))

	err = fc.AddTx(ctx, invalidTransfer)
	if errors.Root(err) != ErrTxRejected {
		t.Fatalf("got err = %q want %q", errors.Root(err), ErrTxRejected)
	}
}

type issuedTestStore struct {
	memstore.MemStore
	f func(map[bc.AssetID]uint64)
}

func (i *issuedTestStore) ApplyTx(ctx context.Context, tx *bc.Tx, issued map[bc.AssetID]uint64) error {
	err := i.MemStore.ApplyTx(ctx, tx, issued)
	if i.f != nil {
		i.f(issued)
	}
	return err
}

func TestAddTxIssued(t *testing.T) {
	ctx := context.Background()

	asset0 := fedtest.Asset(t)
	asset1 := fedtest.Asset(t)
	dest0 := fedtest.Dest(t)
	dest1 := fedtest.Dest(t)

	basicIssue, _, _ := fedtest.Issue(t, asset0, dest0, 10)
	basicTransfer := fedtest.Transfer(t, fedtest.StateOut(basicIssue, 0), dest0, dest1)

	multiIssueData := &bc.TxData{
		Version: bc.CurrentTransactionVersion,
		Inputs: []*bc.TxInput{
			{Previous: bc.Outpoint{Index: bc.InvalidOutputIndex}},
			{Previous: bc.Outpoint{Index: bc.InvalidOutputIndex}},
		},
		Outputs: []*bc.TxOutput{
			{
				Script:      dest0.PKScript,
				AssetAmount: bc.AssetAmount{AssetID: asset0.AssetID, Amount: 2},
			},
			{
				Script:      dest0.PKScript,
				AssetAmount: bc.AssetAmount{AssetID: asset1.AssetID, Amount: 3},
			},
		},
	}
	asset0.Sign(t, multiIssueData, 0, bc.AssetAmount{})
	asset1.Sign(t, multiIssueData, 1, bc.AssetAmount{})
	multiIssue := bc.NewTx(*multiIssueData)

	issueTransferData := &bc.TxData{
		Version: bc.CurrentTransactionVersion,
		Inputs: []*bc.TxInput{
			{Previous: bc.Outpoint{Hash: multiIssue.Hash, Index: 1}},
			{Previous: bc.Outpoint{Index: bc.InvalidOutputIndex}},
		},
		Outputs: []*bc.TxOutput{
			{
				Script:      dest0.PKScript,
				AssetAmount: bc.AssetAmount{AssetID: asset0.AssetID, Amount: 4},
			},
			{
				Script:      dest1.PKScript,
				AssetAmount: bc.AssetAmount{AssetID: asset1.AssetID, Amount: 3},
			},
		},
	}
	dest0.Sign(t, issueTransferData, 0, multiIssue.Outputs[1].AssetAmount)
	asset0.Sign(t, issueTransferData, 1, bc.AssetAmount{})
	issueTransfer := bc.NewTx(*issueTransferData)

	memstore := memstore.New()
	store := &issuedTestStore{
		MemStore: *memstore,
	}
	fc, err := New(ctx, store, nil)
	if err != nil {
		testutil.FatalErr(t, err)
	}

	cases := []struct {
		tx   *bc.Tx
		want map[bc.AssetID]uint64
	}{
		{tx: basicIssue, want: map[bc.AssetID]uint64{asset0.AssetID: 10}},
		{tx: basicTransfer, want: map[bc.AssetID]uint64{}},
		{tx: multiIssue, want: map[bc.AssetID]uint64{asset0.AssetID: 2, asset1.AssetID: 3}},
		{tx: issueTransfer, want: map[bc.AssetID]uint64{asset0.AssetID: 4, asset1.AssetID: 0}},
	}
	for _, c := range cases {
		store.f = func(got map[bc.AssetID]uint64) {
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got issued = %+v want %+v", got, c.want)
			}
		}
		err := fc.AddTx(ctx, c.tx)
		if err != nil {
			testutil.FatalErr(t, err)
		}
	}
}