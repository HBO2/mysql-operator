/*
Copyright 2018 Pressinfra SRL

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package testutil

import (
	"time"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"

	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
)

const (
	drainTimeout = 200 * time.Millisecond
)

// DrainChan drains the request chan time for drainTimeout
func DrainChan(requests <-chan reconcile.Request) {
	for {
		select {
		case <-requests:
			continue
		case <-time.After(drainTimeout):
			return
		}
	}
}

// BackupHaveCondition is a helper func that returns a matcher to check for an
// existing condition in condition list list
func BackupHaveCondition(condType api.BackupConditionType, status core.ConditionStatus) gomegatypes.GomegaMatcher {
	return ContainElement(MatchFields(IgnoreExtras, Fields{
		"Type":   Equal(condType),
		"Status": Equal(status),
	}))
}
