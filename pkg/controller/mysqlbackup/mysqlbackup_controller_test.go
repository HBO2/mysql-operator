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

// nolint: errcheck
package mysqlbackup

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"golang.org/x/net/context"
	batch "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	backupwrap "github.com/presslabs/mysql-operator/pkg/controller/internal/mysqlbackup"
)

const timeout = time.Second * 2

var _ = Describe("MysqlBackup controller", func() {
	var (
		// channel for incoming reconcile requests
		requests chan reconcile.Request
		// stop channel for controller manager
		stop chan struct{}
		// controller k8s client
		c client.Client
	)

	BeforeEach(func() {
		var recFn reconcile.Reconciler

		mgr, err := manager.New(cfg, manager.Options{})
		Expect(err).NotTo(HaveOccurred())
		c = mgr.GetClient()

		recFn, requests = SetupTestReconcile(newReconciler(mgr))
		Expect(add(mgr, recFn)).To(Succeed())

		stop = StartTestManager(mgr)
	})

	AfterEach(func() {
		close(stop)
	})

	// instanciate a cluster and a backup
	var (
		expectedRequest reconcile.Request
		cluster         *api.MysqlCluster
		backup          *api.MysqlBackup
		wBackup         *backupwrap.Wrapper
		backupKey       types.NamespacedName
		jobKey          types.NamespacedName
	)

	BeforeEach(func() {
		clusterName := fmt.Sprintf("cluster-%d", rand.Int31())
		name := fmt.Sprintf("backup-%d", rand.Int31())
		ns := "default"

		backupKey = types.NamespacedName{Name: name, Namespace: ns}
		expectedRequest = reconcile.Request{
			NamespacedName: backupKey,
		}

		cluster = &api.MysqlCluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: ns},
			Spec: api.MysqlClusterSpec{
				Replicas:   2,
				SecretName: "a-secret",
			},
		}

		backup = &api.MysqlBackup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: api.MysqlBackupSpec{
				ClusterName:      clusterName,
				BackupURI:        "gs://bucket/",
				BackupSecretName: "secert",
			},
		}

		wBackup = backupwrap.New(backup)
		jobKey = types.NamespacedName{
			Name:      wBackup.GetNameForJob(),
			Namespace: backup.Namespace,
		}
	})

	When("a new mysql backup is created", func() {

		BeforeEach(func() {
			// create a cluster and a backup
			Expect(c.Create(context.TODO(), cluster)).To(Succeed())
			Expect(c.Create(context.TODO(), backup)).To(Succeed())

			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))
			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))

			// some extra reconcile requests may appear
			drainChan(requests)
			// We need to make sure that the controller does not create infinite
			// loops
			Consistently(requests).ShouldNot(Receive(Equal(expectedRequest)))
		})

		AfterEach(func() {
			c.Delete(context.TODO(), cluster)
			c.Delete(context.TODO(), backup)
		})

		It("should create the job", func() {
			job := &batch.Job{}
			jobKey := types.NamespacedName{
				Name:      wBackup.GetNameForJob(),
				Namespace: backup.Namespace,
			}
			Expect(c.Get(context.TODO(), jobKey, job)).To(Succeed())
			Expect(job.Spec.Template.Spec.Containers[0].Name).To(Equal("backup"))
		})

		It("should populate the defaults", func() {
			Expect(c.Get(context.TODO(), backupKey, backup)).To(Succeed())
			Expect(backup.Spec.BackupURI).To(ContainSubstring(backupwrap.BackupSuffix))
		})
	})

	When("a backup is complete", func() {
		BeforeEach(func() {
			// mark backup as completed
			backup.Status.Completed = true
			// create a cluster and a backup
			Expect(c.Create(context.TODO(), cluster)).To(Succeed())
			Expect(c.Create(context.TODO(), backup)).To(Succeed())

			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))
		})

		AfterEach(func() {
			c.Delete(context.TODO(), cluster)
			c.Delete(context.TODO(), backup)
		})

		It("should skip creating job", func() {
			job := &batch.Job{}
			Expect(c.Get(context.TODO(), jobKey, job)).ToNot(Succeed())
		})

		It("should not receive more reconcile requests", func() {
			Consistently(requests, time.Second).ShouldNot(Receive(Equal(expectedRequest)))
		})
	})

	When("cluster name is not specified", func() {
		BeforeEach(func() {
			// mark backup as completed
			backup.Spec.ClusterName = ""
			// create a cluster and a backup
			Expect(c.Create(context.TODO(), backup)).To(Succeed())
			Expect(c.Create(context.TODO(), cluster)).To(Succeed())

			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))
		})

		AfterEach(func() {
			c.Delete(context.TODO(), cluster)
			c.Delete(context.TODO(), backup)
		})

		It("should skip creating job", func() {
			job := &batch.Job{}
			Expect(c.Get(context.TODO(), jobKey, job)).ToNot(Succeed())
		})

		It("should allow updating cluster name", func() {
			// update cluster
			backup.Spec.ClusterName = cluster.Name
			Expect(c.Update(context.TODO(), backup)).To(Succeed())

			// wait for reconcile request
			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))

			// check for job to be created
			// NOTE: maybe check in an eventually for job to be created.
			job := &batch.Job{}
			Expect(c.Get(context.TODO(), jobKey, job)).To(Succeed())
		})
	})
})

func drainChan(requests <-chan reconcile.Request) {
	for {
		select {
		case <-requests:
			continue
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
