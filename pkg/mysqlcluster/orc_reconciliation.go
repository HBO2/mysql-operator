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
	"context"
	"fmt"
	"time"

	"github.com/golang/glog"
	core "k8s.io/api/core/v1"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	orc "github.com/presslabs/mysql-operator/pkg/util/orchestrator"
)

const (
	healtyMoreThanMinutes        = 10
	defaultMaxSlaveLatency int64 = 30
)

// SyncOrchestratorStatus function is called in a loop and should update cluster status
// with latest information from orchestrator or to register the new nodes into
// orchestrator.
func (f *cFactory) SyncOrchestratorStatus(ctx context.Context) error {
	glog.Infof("Orchestrator reconciliation for cluster '%s' started...", f.cluster.Name)
	if f.orcClient == nil {
		return fmt.Errorf("orchestrator is not configured")
	}

	// Try to get cluster from orchestrator if cluster is not present then
	// register nodes into orchestrator.
	if insts, err := f.orcClient.Cluster(f.getClusterAlias()); err == nil {

		err = f.updateNodesReadOnlyFlagInOrc(insts)
		if err != nil {
			glog.Infof("Error setting Master readOnly/writable %s", err)
		}

		f.updateStatusFromOrc(insts)

	} else {
		glog.Errorf("Fail to get cluster from orchestrator: %s. Now tries to register nodes.", err)
		return f.registerNodesInOrc()
	}

	// Check for orchestrator audit recoveries and acknowledge recoveries.
	if recoveries, err := f.orcClient.AuditRecovery(f.getClusterAlias()); err == nil {
		f.updateStatusForRecoveries(recoveries)
		toAck := f.getRecoveriesToAck(recoveries)

		comment := fmt.Sprintf("Statefulset '%s' is healty more then 10 minutes",
			f.cluster.GetNameForResource(api.StatefulSet),
		)

		// acknowledge recoveries
		for _, r := range toAck {
			if err := f.orcClient.AckRecovery(r.Id, comment); err != nil {
				glog.Errorf("Trying to ack recovery with id %d but failed with error: %s",
					r.Id, err,
				)
			}
			f.rec.Event(f.cluster, core.EventTypeNormal, "RecoveryAcked",
				fmt.Sprintf("Recovery with id %d was acked.", r.Id))
		}
	}

	return nil
}

func getInstance(hostname string, insts []orc.Instance) (*orc.Instance, error) {

	for _, node := range insts {
		host := node.Key.Hostname

		if host == hostname {
			return &node, nil
		}
	}

	return nil, fmt.Errorf("the element was not found")
}

func getMaster(node *orc.Instance, insts []orc.Instance) (*orc.Instance, error) {

	if len(node.MasterKey.Hostname) != 0 && node.IsCoMaster == false {
		next, err := getInstance(node.MasterKey.Hostname, insts)
		if err == nil {
			return getMaster(next, insts)
		} else {
			return nil, err
		}
	}

	if node.IsCoMaster == true {
		next, err := getInstance(node.MasterKey.Hostname, insts)
		if err == nil {
			return next, nil
		} else {
			return nil, err
		}
	}

	return node, nil
}

func determineMasterFor(insts []orc.Instance) (*orc.Instance, error) {

	var masterForNode []orc.Instance

	for _, node := range insts {
		master, err := getMaster(&node, insts)
		if err == nil {
			masterForNode = append(masterForNode, *master)
		} else {
			return nil, fmt.Errorf("not able to retrieve the root of this node %s", node.Key.Hostname)
		}
	}

	if len(masterForNode) != 0 {
		masterHostName := masterForNode[0]
		var check bool = true
		for _, node := range masterForNode {
			if node.Key.Hostname != masterHostName.Key.Hostname {
				check = false
			}
		}
		if check == true {
			return &masterHostName, nil
		} else {
			return nil, fmt.Errorf("multiple masters")
		}
	} else {
		return nil, fmt.Errorf("0 elements in instance array")
	}

}

// set a host writable just if needed
func (f *cFactory) setInstWritable(inst orc.Instance) error {
	if inst.ReadOnly == true {
		glog.V(2).Infof("set instance %s writable", inst.Key.Hostname)
		return f.orcClient.SetHostWritable(inst.Key)
	}
	return nil
}

func (f *cFactory) putNodeInMaintenance(inst orc.Instance) error {

	glog.V(2).Infof("set instance %s in maintenance", inst.Key.Hostname)
	return f.orcClient.BeginMaintenance(inst.Key, "mysqlcontroller", "clusterReadOnly")

}

func (f *cFactory) getNodeOutOfMaintenance(inst orc.Instance) error {

	glog.V(2).Infof("set instance %s out of maintenance", inst.Key.Hostname)
	return f.orcClient.EndMaintenance(inst.Key)

}

// set a host read only just if needed
func (f *cFactory) setInstReadOnly(inst orc.Instance) error {
	if !inst.ReadOnly == true {
		glog.V(2).Infof("set instance %s read only", inst.Key.Hostname)
		return f.orcClient.SetHostReadOnly(inst.Key)
	}
	return nil
}

func (f *cFactory) updateNodesReadOnlyFlagInOrc(insts []orc.Instance) error {
	master, err := determineMasterFor(insts)
	if err != nil && err.Error() == "multiple masters" {
		// master is not found
		// set cluster read only
		for _, inst := range insts {
			f.putNodeInMaintenance(inst)
			f.setInstReadOnly(inst)
		}
		return nil
	} else if err != nil {
		return err
	}

	// master is determinated
	for _, inst := range insts {
		if f.cluster.Spec.ReadOnly == true {
			f.putNodeInMaintenance(inst)
			f.setInstReadOnly(inst)
		} else if f.cluster.Spec.ReadOnly == false && err == nil {
			f.getNodeOutOfMaintenance(inst)
			if inst.Key.Hostname == master.Key.Hostname {
				f.setInstWritable(inst)
			} else {
				f.setInstReadOnly(inst)
			}
		}
	}

	return nil
}

func (f *cFactory) updateStatusFromOrc(insts []orc.Instance) {
	// TODO: imporve this code by computing differences between what
	// orchestartor knows and what we know

	updatedNodes := []string{}

	var isReadOnly bool = true
	for _, node := range insts {
		host := node.Key.Hostname
		updatedNodes = append(updatedNodes, host)

		if !node.IsUpToDate {
			if !node.IsLastCheckValid {
				f.updateNodeCondition(host, api.NodeConditionLagged, core.ConditionUnknown)
				f.updateNodeCondition(host, api.NodeConditionReplicating, core.ConditionUnknown)
				f.updateNodeCondition(host, api.NodeConditionMaster, core.ConditionUnknown)
			}
			continue
		}
		maxSlaveLatency := defaultMaxSlaveLatency
		if f.cluster.Spec.MaxSlaveLatency != nil {
			maxSlaveLatency = *f.cluster.Spec.MaxSlaveLatency
		}
		if !node.SlaveLagSeconds.Valid {
			f.updateNodeCondition(host, api.NodeConditionLagged, core.ConditionUnknown)
		} else if node.SlaveLagSeconds.Int64 <= maxSlaveLatency {
			f.updateNodeCondition(host, api.NodeConditionLagged, core.ConditionFalse)
		} else { // node is behind master
			f.updateNodeCondition(host, api.NodeConditionLagged, core.ConditionTrue)
		}
		if node.Slave_SQL_Running && node.Slave_IO_Running {
			f.updateNodeCondition(host, api.NodeConditionReplicating, core.ConditionTrue)
		} else {
			f.updateNodeCondition(host, api.NodeConditionReplicating, core.ConditionFalse)
		}
		f.updateNodeCondition(host, api.NodeConditionMaster, core.ConditionFalse)
		isReadOnly = isReadOnly && node.ReadOnly
		if node.ReadOnly == true {
			f.updateNodeCondition(host, api.NodeConditionReadOnly, core.ConditionTrue)
		} else {
			f.updateNodeCondition(host, api.NodeConditionReadOnly, core.ConditionFalse)
		}
	}

	master, err := determineMasterFor(insts)
	if err != nil {
		glog.Errorf("Error acquiring master name %s", err)
	} else {
		f.updateNodeCondition(master.Key.Hostname, api.NodeConditionMaster, core.ConditionTrue)

		if isReadOnly == true {
			f.cluster.UpdateStatusCondition(api.ClusterConditionReadOnly,
				core.ConditionTrue, "initializedTrue", "settingReadOnlyTrue")
		} else {
			f.cluster.UpdateStatusCondition(api.ClusterConditionReadOnly,
				core.ConditionFalse, "initializedFalse", "settingReadOnlyFalse")
		}
	}

	f.removeNodeConditionNotIn(updatedNodes)
}

func (f *cFactory) updateStatusForRecoveries(recoveries []orc.TopologyRecovery) {
	var unack []orc.TopologyRecovery
	for _, recovery := range recoveries {
		if !recovery.Acknowledged {
			unack = append(unack, recovery)
		}
	}

	if len(unack) > 0 {
		msg := getRecoveryTextMsg(unack)
		f.cluster.UpdateStatusCondition(api.ClusterConditionFailoverAck,
			core.ConditionTrue, "pendingFailoverAckExists", msg)
	} else {
		f.cluster.UpdateStatusCondition(api.ClusterConditionFailoverAck,
			core.ConditionFalse, "noPendingFailoverAckExists", "no pending ack")
	}
}

func (f *cFactory) registerNodesInOrc() error {
	// Register nodes in orchestrator
	// try to discover ready nodes into orchestrator
	for i := 0; i < int(f.cluster.Status.ReadyNodes); i++ {
		host := f.cluster.GetPodHostname(i)
		if err := f.orcClient.Discover(host, MysqlPort); err != nil {
			glog.Warningf("Failed to register %s with orchestrator: %s", host, err.Error())
		}
	}

	return nil
}

func (f *cFactory) getRecoveriesToAck(recoveries []orc.TopologyRecovery) (toAck []orc.TopologyRecovery) {
	// TODO: check for recoveries that need acknowledge, by excluding already acked recoveries
	if len(recoveries) == 0 {
		return
	}

	i, find := condIndexCluster(f.cluster, api.ClusterConditionReady)
	if !find || f.cluster.Status.Conditions[i].Status != core.ConditionTrue {
		glog.Warning("[getRecoveriesToAck]: Cluster is not ready for ack.")
		return
	}

	if time.Since(f.cluster.Status.Conditions[i].LastTransitionTime.Time).Minutes() < healtyMoreThanMinutes {
		glog.Warning(
			"[getRecoveriesToAck]: Stateful set is not ready more then 10 minutes. Don't ack.",
		)
		return
	}

	for _, recovery := range recoveries {
		if !recovery.Acknowledged {
			// skip if it's a new recovery, recovery should be older then <healtyMoreThanMinutes> minutes
			startTime, err := time.Parse(time.RFC3339, recovery.RecoveryStartTimestamp)
			if err != nil {
				glog.Errorf("[getRecoveriesToAck] Can't parse time: %s for audit recovery: %d",
					err, recovery.Id,
				)
				continue
			}
			if time.Since(startTime).Minutes() < healtyMoreThanMinutes {
				// skip this recovery
				glog.Errorf("[getRecoveriesToAck] recovery to soon")
				continue
			}

			toAck = append(toAck, recovery)
		}
	}
	return
}

func condIndexCluster(r *api.MysqlCluster, ty api.ClusterConditionType) (int, bool) {
	for i, cond := range r.Status.Conditions {
		if cond.Type == ty {
			return i, true
		}
	}

	return 0, false
}

func (f *cFactory) updateNodeCondition(host string, cType api.NodeConditionType, status core.ConditionStatus) {
	i := f.cluster.Status.GetNodeStatusIndex(host)
	changed := f.cluster.Status.Nodes[i].UpdateNodeCondition(cType, status)
	if !changed {
		return
	}

	pod, err := getPodForHostname(f.client, f.namespace, f.getLabels(map[string]string{}), host)
	if err != nil {
		glog.Errorf("Can't get pod for hostname %s, error: %s", host, err)
		return
	}

	switch cType {
	case api.NodeConditionMaster:
		if status == core.ConditionTrue {
			f.rec.Event(pod, core.EventTypeWarning, "PromoteMaster", "Promoted as master by orchestrator")
		} else if status == core.ConditionFalse {
			f.rec.Event(pod, core.EventTypeWarning, "DemoteMaster", "Demoted as master by orchestrator")
		}
	case api.NodeConditionLagged:
		if status == core.ConditionTrue {
			f.rec.Event(pod, core.EventTypeNormal, "LagDetected", "This node has lag. Lag was detected.")
		}
	case api.NodeConditionReplicating:
		if status == core.ConditionTrue {
			f.rec.Event(pod, core.EventTypeNormal, "ReplicationRunning", "Replication is running")
		} else if status == core.ConditionFalse {
			f.rec.Event(pod, core.EventTypeWarning, "ReplicationStopped", "Replication is stopped")
		}
	}
}

func (f *cFactory) removeNodeConditionNotIn(hosts []string) {
	for _, ns := range f.cluster.Status.Nodes {
		updated := false
		for _, h := range hosts {
			if h == ns.Name {
				updated = true
			}
		}

		if !updated {
			f.updateNodeCondition(ns.Name, api.NodeConditionLagged, core.ConditionUnknown)
			f.updateNodeCondition(ns.Name, api.NodeConditionReplicating, core.ConditionUnknown)
			f.updateNodeCondition(ns.Name, api.NodeConditionMaster, core.ConditionUnknown)
		}
	}
}
