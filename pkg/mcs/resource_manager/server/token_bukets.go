// Copyright 2022 TiKV Project Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,g
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"time"

	"github.com/gogo/protobuf/proto"
	rmpb "github.com/pingcap/kvproto/pkg/resource_manager"
)

const defaultRefillRate = 10000

const defaultInitialTokens = 10 * 10000

const defaultMaxTokens = 1e7

const defaultLoanMaxPeriod = 10 * time.Second

var loanReserveRatio float64 = 0.05

// GroupTokenBucket is a token bucket for a resource group.
// TODO: statistics Consumption
type GroupTokenBucket struct {
	*rmpb.TokenBucket `json:"token_bucket,omitempty"`
	// LoanMaxPeriod represents the maximum loan period, which together with the fill rate determines the loan amount
	LoanMaxPeriod time.Duration `json:"loan_max_perio,omitempty"`
	// MaxTokens limits the number of tokens that can be accumulated
	MaxTokens float64 `json:"max_tokens,omitempty"`

	Consumption    *rmpb.TokenBucketsRequest `json:"consumption,omitempty"`
	LastUpdate     *time.Time                `json:"last_update,omitempty"`
	Initialized    bool                      `json:"initialized"`
	LoanExpireTime *time.Time                `json:"loan_time,omitempty"`
}

// NewGroupTokenBucket returns a new GroupTokenBucket
func NewGroupTokenBucket(tokenBucket *rmpb.TokenBucket) GroupTokenBucket {
	return GroupTokenBucket{
		TokenBucket:   tokenBucket,
		MaxTokens:     defaultMaxTokens,
		LoanMaxPeriod: defaultLoanMaxPeriod,
	}
}

// patch patches the token bucket settings.
func (t *GroupTokenBucket) patch(settings *rmpb.TokenBucket) {
	if settings == nil {
		return
	}
	tb := proto.Clone(t.TokenBucket).(*rmpb.TokenBucket)
	if settings.GetSettings() != nil {
		if tb == nil {
			tb = &rmpb.TokenBucket{}
		}
		tb.Settings = settings.GetSettings()
	}

	// the settings in token is delta of the last update and now.
	tb.Tokens += settings.GetTokens()
	t.TokenBucket = tb
}

// update updates the token bucket.
func (t *GroupTokenBucket) update(now time.Time) {
	if !t.Initialized {
		if t.Settings.FillRate == 0 {
			t.Settings.FillRate = defaultRefillRate
		}
		if t.Tokens < defaultInitialTokens {
			t.Tokens = defaultInitialTokens
		}
		t.LastUpdate = &now
		t.Initialized = true
		return
	}

	delta := now.Sub(*t.LastUpdate)
	if delta > 0 {
		t.Tokens += float64(t.Settings.FillRate) * delta.Seconds()
		t.LastUpdate = &now
	}
	if t.Tokens >= 0 {
		t.LoanExpireTime = nil
	}
	if t.Tokens > t.MaxTokens {
		t.Tokens = t.MaxTokens
	}
}

// request requests tokens from the token bucket.
func (t *GroupTokenBucket) request(
	neededTokens float64, targetPeriodMs uint64,
) (*rmpb.TokenBucket, int64) {
	var res rmpb.TokenBucket
	res.Settings = &rmpb.TokenLimitSettings{}
	// FillRate is used for the token server unavailable in abnormal situation.
	res.Settings.FillRate = 0
	if neededTokens <= 0 {
		return &res, 0
	}

	// If the current tokens can directly meet the requirement, returns the need token
	if t.Tokens >= neededTokens {
		t.Tokens -= neededTokens
		// granted the total request tokens
		res.Tokens = neededTokens
		return &res, 0
	}

	// Firstly allocate the remaining tokens
	var grantedTokens float64
	if t.Tokens > 0 {
		grantedTokens = t.Tokens
		t.Tokens = 0
		neededTokens -= grantedTokens
	}

	// Consider using a loan to get tokens
	var periodFilled float64
	var trickleTime = time.Duration(targetPeriodMs) * time.Millisecond
	// If the loan has been used, and within the expiration date of the loan,
	// We calculate `periodFilled` which is the number of tokens that can be allocated
	// according to fillrate, remaining loan time and retention ratio
	if t.LoanExpireTime != nil {
		if t.LoanExpireTime.After(*t.LastUpdate) {
			duration := t.LoanExpireTime.Sub(*t.LastUpdate)
			periodFilled = float64(t.Settings.FillRate) * (1 - loanReserveRatio) * duration.Seconds()
			trickleTime = duration
		}
	} else { // Apply for a loan
		et := t.LastUpdate.Add(t.LoanMaxPeriod)
		t.LoanExpireTime = &et
		periodFilled = float64(t.Settings.FillRate) * (1 - loanReserveRatio) * t.LoanMaxPeriod.Seconds()
	}
	// have to deduct what resource group already owe
	periodFilled += t.Tokens
	if periodFilled <= float64(t.Settings.FillRate)*loanReserveRatio*trickleTime.Seconds() {
		periodFilled = float64(t.Settings.FillRate) * loanReserveRatio * trickleTime.Seconds()
	}
	if periodFilled >= neededTokens {
		grantedTokens += neededTokens
		t.Tokens -= neededTokens
	} else {
		grantedTokens += periodFilled
		t.Tokens -= periodFilled
	}
	res.Tokens = grantedTokens
	return &res, trickleTime.Milliseconds()
}
