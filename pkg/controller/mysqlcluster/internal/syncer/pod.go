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

package mysqlcluster

import (
	"strings"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/presslabs/controller-util/syncer"
	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	clusterwrap "github.com/presslabs/mysql-operator/pkg/controller/internal/mysqlcluster"
)

type podSyncer struct {
	cluster  *clusterwrap.MysqlCluster
	hostname string
	pod      *core.Pod
}

const (
	labelMaster    = "master"
	labelReplica   = "replica"
	labelHealty    = "yes"
	labelNotHealty = "no"
)

// NewPodSyncer returns the syncer for pod
func NewPodSyncer(cluster *api.MysqlCluster, host string) syncer.Interface {
	obj := &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getPodNameForHost(host),
			Namespace: cluster.Namespace,
		},
	}

	return &podSyncer{
		cluster:  clusterwrap.NewMysqlClusterWrapper(cluster),
		pod:      obj,
		hostname: host,
	}
}

func (s *podSyncer) GetObject() runtime.Object { return s.pod }
func (s *podSyncer) GetOwner() runtime.Object  { return nil }
func (s *podSyncer) GetEventReasonForError(err error) syncer.EventReason {
	return syncer.BasicEventReason("Pod", err)
}

func (s *podSyncer) SyncFn(in runtime.Object) error {
	out := in.(*core.Pod)

	// do nothing if the pod is not created
	if !out.CreationTimestamp.IsZero() {
		return nil
	}

	isMaster := condToBool(s.cluster.GetNodeCondition(s.hostname, api.NodeConditionMaster))
	isReplicating := condToBool(s.cluster.GetNodeCondition(s.hostname, api.NodeConditionReplicating))
	isLagged := condToBool(s.cluster.GetNodeCondition(s.hostname, api.NodeConditionLagged))

	// set role label
	role := labelReplica
	if isMaster {
		role = labelMaster
	}

	// set healty label
	healty := labelHealty
	if isLagged || !isMaster && !isReplicating {
		healty = labelNotHealty
	}

	out.ObjectMeta.Labels["role"] = role
	out.ObjectMeta.Labels["healty"] = healty

	return nil
}

func condToBool(cond *api.NodeCondition) bool {
	return cond != nil && cond.Status == core.ConditionTrue
}

func getPodNameForHost(host string) string {
	return strings.SplitN(host, ".", 1)[0]
}
