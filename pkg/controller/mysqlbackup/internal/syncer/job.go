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

package syncer

import (
	"fmt"
	"strings"
	"time"

	"github.com/presslabs/controller-util/syncer"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	backupwrap "github.com/presslabs/mysql-operator/pkg/controller/internal/mysqlbackup"
	"github.com/presslabs/mysql-operator/pkg/options"
)

type jobSyncer struct {
	backup  *backupwrap.MysqlBackup
	cluster *api.MysqlCluster

	job *batch.Job
	opt *options.Options
}

// NewJobSyncer returns a syncer for backup jobs
func NewJobSyncer(backup *api.MysqlBackup, cluster *api.MysqlCluster, opt *options.Options) syncer.Interface {

	obj := &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-backupjob", backup.Name),
			Namespace: backup.Namespace,
		},
	}

	return &jobSyncer{
		backup:  backupwrap.New(backup),
		cluster: cluster,
		job:     obj,
		opt:     opt,
	}
}

func (s *jobSyncer) GetObject() runtime.Object { return s.job }
func (s *jobSyncer) GetOwner() runtime.Object  { return s.backup }
func (s *jobSyncer) GetEventReasonForError(err error) syncer.EventReason {
	return syncer.BasicEventReason("Job", err)
}

func (s *jobSyncer) SyncFn(in runtime.Object) error {
	out := in.(*batch.Job)

	// check if job is already created an just update the status
	if out.ObjectMeta.CreationTimestamp.IsZero() {
		s.updateStatus(out)
		return nil
	}

	if len(s.getBackupURI()) == 0 {
		return fmt.Errorf("backupURI not specified")
	}

	out.Labels = map[string]string{
		"cluster": s.backup.Spec.ClusterName,
	}

	out.Spec.Template.Spec = s.ensurePodSpec(out.Spec.Template.Spec)
	return nil
}

func (s *jobSyncer) getBackupURI() string {
	if len(s.backup.Status.BackupURI) > 0 {
		return s.backup.Status.BackupURI
	}

	if len(s.backup.Spec.BackupURI) > 0 {
		return s.backup.Spec.BackupURI
	}

	if len(s.cluster.Spec.BackupURI) > 0 {
		return getBucketURI(s.cluster.Name, s.cluster.Spec.BackupURI)
	}

	return ""
}

func getBucketURI(cluster, bucket string) string {
	if strings.HasSuffix(bucket, "/") {
		bucket = bucket[:len(bucket)-1]
	}
	t := time.Now()
	return bucket + fmt.Sprintf(
		"/%s-%s.xbackup.gz", cluster, t.Format("2006-01-02T15:04:05"),
	)
}

func (s *jobSyncer) getBackupSecretName() string {
	if len(s.backup.Spec.BackupSecretName) > 0 {
		return s.backup.Spec.BackupSecretName
	}

	return s.cluster.Spec.BackupSecretName
}

func (s *jobSyncer) getBackupCandidate() string {
	// TODO: cluster.GetBackupCandidate()
	return ""
}

func (s *jobSyncer) ensurePodSpec(in core.PodSpec) core.PodSpec {
	if len(in.Containers) == 0 {
		in.Containers = make([]core.Container, 1)
	}

	in.RestartPolicy = core.RestartPolicyNever

	in.Containers[0].Name = "backup"
	in.Containers[0].Image = s.opt.HelperImage
	in.Containers[0].ImagePullPolicy = core.PullIfNotPresent
	in.Containers[0].Args = []string{
		"take-backup-to",
		s.getBackupCandidate(),
		s.getBackupURI(),
	}

	boolTrue := true
	in.Containers[0].Env = []core.EnvVar{
		core.EnvVar{
			Name: "MYSQL_BACKUP_USER",
			ValueFrom: &core.EnvVarSource{
				SecretKeyRef: &core.SecretKeySelector{
					LocalObjectReference: core.LocalObjectReference{
						Name: s.cluster.Spec.SecretName,
					},
					Key:      "BACKUP_USER",
					Optional: &boolTrue,
				},
			},
		},
		core.EnvVar{
			Name: "MYSQL_BACKUP_PASSWORD",
			ValueFrom: &core.EnvVarSource{
				SecretKeyRef: &core.SecretKeySelector{
					LocalObjectReference: core.LocalObjectReference{
						Name: s.cluster.Spec.SecretName,
					},
					Key:      "BACKUP_PASSWORD",
					Optional: &boolTrue,
				},
			},
		},
	}

	if len(s.getBackupSecretName()) != 0 {
		in.Containers[0].EnvFrom = []core.EnvFromSource{
			core.EnvFromSource{
				SecretRef: &core.SecretEnvSource{
					LocalObjectReference: core.LocalObjectReference{
						Name: s.getBackupSecretName(),
					},
				},
			},
		}
	}
	return in
}

func (s *jobSyncer) updateStatus(job *batch.Job) {
	// check for completion condition
	if cond := jobCondition(batch.JobComplete, job); cond != nil {
		s.backup.UpdateStatusCondition(api.BackupComplete, cond.Status, cond.Reason, cond.Message)

		if cond.Status == core.ConditionTrue {
			s.backup.Status.Completed = true
		}
	}

	// check for failed condition
	if cond := jobCondition(batch.JobFailed, job); cond != nil {
		s.backup.UpdateStatusCondition(api.BackupFailed, cond.Status, cond.Reason, cond.Message)
	}
}

func jobCondition(condType batch.JobConditionType, job *batch.Job) *batch.JobCondition {
	for _, c := range job.Status.Conditions {
		if c.Type == condType {
			return &c
		}
	}

	return nil
}
