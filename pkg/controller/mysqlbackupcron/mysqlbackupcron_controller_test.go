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
package mysqlbackupcron

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	cronpkg "github.com/wgliang/cron"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
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
		// cron job
		cron *cronpkg.Cron
	)

	BeforeEach(func() {
		var recFn reconcile.Reconciler
		cron = cronpkg.New()

		mgr, err := manager.New(cfg, manager.Options{})
		Expect(err).NotTo(HaveOccurred())
		c = mgr.GetClient()

		recFn, requests = SetupTestReconcile(newReconciler(mgr, cron))
		Expect(add(mgr, recFn)).To(Succeed())

		stop = StartTestManager(mgr)
	})

	AfterEach(func() {
		close(stop)
	})

	Describe("when creating a new mysql cluster", func() {
		var (
			expectedRequest reconcile.Request
			cluster         *api.MysqlCluster
			secret          *corev1.Secret
		)

		BeforeEach(func() {
			name := fmt.Sprintf("cluster-%d", rand.Int31())
			ns := "default"

			expectedRequest = reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
			}

			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "the-secret", Namespace: ns},
				StringData: map[string]string{
					"ROOT_PASSWORD": "this-is-secret",
				},
			}

			cluster = &api.MysqlCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
				Spec: api.MysqlClusterSpec{
					Replicas:   2,
					SecretName: secret.Name,
				},
			}

			Expect(c.Create(context.TODO(), secret)).To(Succeed())
			Expect(c.Create(context.TODO(), cluster)).To(Succeed())

			// Initial reconciliation
			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))
			// Reconcile triggered by components being created and status being
			// updated
			Eventually(requests, timeout).Should(Receive(Equal(expectedRequest)))

			// some extra reconcile requests may appear
		drain:
			for {
				select {
				case <-requests:
					continue
				case <-time.After(100 * time.Millisecond):
					break drain
				}
			}

			// We need to make sure that the controller does not create infinite
			// loops
			Consistently(requests).ShouldNot(Receive(Equal(expectedRequest)))
		})

		AfterEach(func() {
			c.Delete(context.TODO(), secret)
			c.Delete(context.TODO(), cluster)
		})

	})
})
