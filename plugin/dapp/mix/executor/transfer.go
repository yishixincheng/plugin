// Copyright Fuzamei Corp. 2018 All Rights Reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package executor

import (
	"github.com/consensys/gnark/frontend"

	"github.com/33cn/chain33/common/address"

	"github.com/33cn/chain33/types"
	mixTy "github.com/33cn/plugin/plugin/dapp/mix/types"
	"github.com/consensys/gnark-crypto/ecc/bn254/twistededwards"

	dbm "github.com/33cn/chain33/common/db"
	"github.com/pkg/errors"
)

/*
1. verify(zk-proof)
2. check if exist in authorize pool and nullifier pool

*/
func transferInput(db dbm.KV, execer, symbol string, proof *mixTy.ZkProofInfo) (*mixTy.TransferInputCircuit, error) {
	var input mixTy.TransferInputCircuit
	err := mixTy.ConstructCircuitPubInput(proof.PublicInput, &input)
	if err != nil {
		return nil, errors.Wrapf(err, "decode string=%s", proof.PublicInput)
	}

	treeRootHash := frontend.FromInterface(frontend.GetAssignedValue(input.TreeRootHash))
	nullifierHash := frontend.FromInterface(frontend.GetAssignedValue(input.NullifierHash))
	authSpendHash := frontend.FromInterface(frontend.GetAssignedValue(input.AuthorizeSpendHash))
	err = spendVerify(db, execer, symbol, treeRootHash.String(), nullifierHash.String(), authSpendHash.String())
	if err != nil {
		return nil, errors.Wrap(err, "transferInput verify spendVerify")
	}

	err = zkProofVerify(db, proof, mixTy.VerifyType_TRANSFERINPUT)
	if err != nil {
		return nil, errors.Wrap(err, "transferInput verify proof verify")
	}

	return &input, nil

}

/*
1. verify(zk-proof)
2. check if exist in authorize pool and nullifier pool

*/
func transferOutputVerify(db dbm.KV, proof *mixTy.ZkProofInfo) (*mixTy.TransferOutputCircuit, error) {
	var input mixTy.TransferOutputCircuit
	err := mixTy.ConstructCircuitPubInput(proof.PublicInput, &input)
	if err != nil {
		return nil, errors.Wrapf(err, "decode string=%s", proof.PublicInput)
	}
	err = zkProofVerify(db, proof, mixTy.VerifyType_TRANSFEROUTPUT)
	if err != nil {
		return nil, errors.Wrap(err, "Output verify proof verify")
	}

	return &input, nil

}

func VerifyCommitValues(inputs []*mixTy.TransferInputCircuit, outputs []*mixTy.TransferOutputCircuit, txFee uint64) bool {
	var inputPoints, outputPoints []*twistededwards.PointAffine
	for _, in := range inputs {
		var p twistededwards.PointAffine
		p.X.SetInterface(frontend.GetAssignedValue(in.ShieldAmountX))
		p.Y.SetInterface(frontend.GetAssignedValue(in.ShieldAmountY))
		inputPoints = append(inputPoints, &p)
	}

	for _, out := range outputs {
		var p twistededwards.PointAffine
		p.X.SetInterface(frontend.GetAssignedValue(out.ShieldAmountX))
		p.Y.SetInterface(frontend.GetAssignedValue(out.ShieldAmountY))
		outputPoints = append(outputPoints, &p)
	}
	//out value add fee
	//对于平行链来说， 隐私交易需要一个公共账户扣主链的手续费，隐私交易只需要扣平行链执行器内的费用即可
	//由于平行链的隐私交易没有实际扣平行链mix合约的手续费，平行链Mix合约会有手续费留下，平行链隐私可以考虑手续费为0
	outputPoints = append(outputPoints, mixTy.MulCurvePointG(txFee))

	//sum input and output
	sumInput := inputPoints[0]
	sumOutput := outputPoints[0]
	for _, p := range inputPoints[1:] {
		sumInput.Add(sumInput, p)
	}
	for _, p := range outputPoints[1:] {
		sumOutput.Add(sumOutput, p)
	}

	if sumInput.X.Equal(&sumOutput.X) && sumInput.Y.Equal(&sumOutput.Y) {
		return true
	}
	return false
}

func MixTransferInfoVerify(cfg *types.Chain33Config, db dbm.KV, transfer *mixTy.MixTransferAction) ([]*mixTy.TransferInputCircuit, []*mixTy.TransferOutputCircuit, error) {
	var inputs []*mixTy.TransferInputCircuit
	var outputs []*mixTy.TransferOutputCircuit

	execer, symbol := mixTy.GetAssetExecSymbol(cfg, transfer.AssetExec, transfer.AssetSymbol)
	txFee := mixTy.GetTransferTxFee(cfg, execer)
	//inputs
	for _, i := range transfer.Inputs {
		in, err := transferInput(db, execer, symbol, i)
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, in)

	}

	//output
	out, err := transferOutputVerify(db, transfer.Output)
	if err != nil {
		return nil, nil, err
	}
	outputs = append(outputs, out)

	//change
	change, err := transferOutputVerify(db, transfer.Change)
	if err != nil {
		return nil, nil, err
	}
	outputs = append(outputs, change)

	if !VerifyCommitValues(inputs, outputs, uint64(txFee)) {
		return nil, nil, errors.Wrap(mixTy.ErrSpendInOutValueNotMatch, "verify shieldValue")
	}

	return inputs, outputs, nil
}

//1. 如果
func (a *action) processTransferFee(exec, symbol string) (*types.Receipt, error) {
	cfg := a.api.GetConfig()
	accoutDb, err := createAccount(cfg, exec, symbol, a.db)
	if err != nil {
		return nil, err
	}
	txFee := mixTy.GetTransferTxFee(cfg, exec)
	execAddr := address.ExecAddress(string(a.tx.Execer))
	//需要mix执行器下的mix账户扣fee， 和mix 扣coins或token手续费保持一致，不然会看到mix的coins账户下和mix的mix账户下不一致
	accFrom := accoutDb.LoadExecAccount(execAddr, execAddr)

	if accFrom.GetBalance()-txFee >= 0 {
		copyfrom := *accFrom
		accFrom.Balance = accFrom.GetBalance() - txFee
		receiptBalance := &types.ReceiptAccountTransfer{Prev: &copyfrom, Current: accFrom}
		set := accoutDb.GetExecKVSet(execAddr, accFrom)

		feelog := &types.ReceiptLog{Ty: types.TyLogFee, Log: types.Encode(receiptBalance)}
		return &types.Receipt{
			Ty:   types.ExecOk,
			KV:   set,
			Logs: append([]*types.ReceiptLog{}, feelog),
		}, nil

	}
	return nil, types.ErrNoBalance
}

/*
1. verify(zk-proof, sum value of spend and new commits)
2. check if exist in authorize pool and nullifier pool
3. add nullifier to pool
*/
func (a *action) Transfer(transfer *mixTy.MixTransferAction) (*types.Receipt, error) {
	inputs, outputs, err := MixTransferInfoVerify(a.api.GetConfig(), a.db, transfer)
	if err != nil {
		return nil, errors.Wrap(err, "Transfer.MixTransferInfoVerify")
	}

	receipt := &types.Receipt{Ty: types.ExecOk}

	execer, symbol := mixTy.GetAssetExecSymbol(a.api.GetConfig(), transfer.AssetExec, transfer.AssetSymbol)
	//扣除交易费
	rTxFee, err := a.processTransferFee(execer, symbol)
	if err != nil {
		return nil, errors.Wrapf(err, "processTransferFee fail")
	}
	mergeReceipt(receipt, rTxFee)

	for _, k := range inputs {
		nullHash := frontend.FromInterface(frontend.GetAssignedValue(k.NullifierHash))
		r := makeNullifierSetReceipt(nullHash.String(), &mixTy.ExistValue{Nullifier: nullHash.String(), Exist: true})
		mergeReceipt(receipt, r)
	}

	//push new commit to merkle tree
	var leaves [][]byte
	for _, h := range outputs {
		noteHash := frontend.FromInterface(frontend.GetAssignedValue(h.NoteHash))
		leaves = append(leaves, mixTy.Str2Byte(noteHash.String()))
	}

	conf := types.ConfSub(a.api.GetConfig(), mixTy.MixX)
	maxTreeLeaves := conf.GInt("maxTreeLeaves")
	rpt, err := pushTree(a.db, execer, symbol, leaves, int32(maxTreeLeaves))
	if err != nil {
		return nil, errors.Wrap(err, "transfer.pushTree")
	}
	mergeReceipt(receipt, rpt)
	return receipt, nil

}
