// Code generated by github.com/fjl/gencodec. DO NOT EDIT.

package types

import (
	"encoding/json"
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

var _ = (*consolidationRequestMarshaling)(nil)

// MarshalJSON marshals as JSON.
func (c ConsolidationRequest) MarshalJSON() ([]byte, error) {
	type ConsolidationRequest struct {
		Source          common.Address `json:"sourceAddress"`
		SourcePublicKey hexutil.Bytes  `json:"sourcePubkey"`
		TargetPublicKey hexutil.Bytes  `json:"targetPubkey"`
	}
	var enc ConsolidationRequest
	enc.Source = c.Source
	enc.SourcePublicKey = c.SourcePublicKey[:]
	enc.TargetPublicKey = c.TargetPublicKey[:]
	return json.Marshal(&enc)
}

// UnmarshalJSON unmarshals from JSON.
func (c *ConsolidationRequest) UnmarshalJSON(input []byte) error {
	type ConsolidationRequest struct {
		Source          *common.Address `json:"sourceAddress"`
		SourcePublicKey *hexutil.Bytes  `json:"sourcePubkey"`
		TargetPublicKey *hexutil.Bytes  `json:"targetPubkey"`
	}
	var dec ConsolidationRequest
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.Source != nil {
		c.Source = *dec.Source
	}
	if dec.SourcePublicKey != nil {
		if len(*dec.SourcePublicKey) != len(c.SourcePublicKey) {
			return errors.New("field 'sourcePubkey' has wrong length, need 48 items")
		}
		copy(c.SourcePublicKey[:], *dec.SourcePublicKey)
	}
	if dec.TargetPublicKey != nil {
		if len(*dec.TargetPublicKey) != len(c.TargetPublicKey) {
			return errors.New("field 'targetPubkey' has wrong length, need 48 items")
		}
		copy(c.TargetPublicKey[:], *dec.TargetPublicKey)
	}
	return nil
}
