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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	mysqlv1alpha1 "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	//"github.com/presslabs/mysql-operator/pkg/handler"
)

const (
	controllerName = "orchestrator-mysql-controller"
	// the period of time in which reconciliation for a cluster is done
	reconcilePeriod = 5
)

var log = logf.Log.WithName(controllerName)

type eventLockMap struct {
	lock sync.RWMutex
	Map  map[string]event.GenericEvent
}

func NewEventLockMap() eventLockMap {
	return eventLockMap{
		Map: make(map[string]event.GenericEvent),
	}
}

var clustersMap = NewEventLockMap()

func (m *eventLockMap) getKey(meta metav1.Object) string {
	return fmt.Sprintf("%s/%s", meta.GetNamespace(), meta.GetName())
}

func (m *eventLockMap) CreateEvent(evt event.CreateEvent) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.Map[m.getKey(evt.Meta)] = event.GenericEvent{
		Meta:   evt.Meta,
		Object: evt.Object,
	}
}

func (m *eventLockMap) DeleteEvent(evt event.DeleteEvent) {
	m.lock.Lock()
	defer m.lock.Unlock()

	_, ok := m.Map[m.getKey(evt.Meta)]
	if ok {
		delete(m.Map, m.getKey(evt.Meta))
	}
}

// Add creates a new MysqlCluster Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
// USER ACTION REQUIRED: update cmd/manager/main.go to call this mysql.Add(mgr) to install this Controller
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMysqlCluster{Client: mgr.GetClient(), scheme: mgr.GetScheme(),
		recorder: mgr.GetRecorder(controllerName)}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MysqlCluster
	err = c.Watch(&source.Kind{Type: &mysqlv1alpha1.MysqlCluster{}}, &handler.Funcs{
		CreateFunc: func(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
			if evt.Meta == nil {
				log.Error(nil, "CreateEvent received with no metadata", "CreateEvent", evt)
				return
			}

			clustersMap.CreateEvent(evt)
		},
		DeleteFunc: func(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
			if evt.Meta == nil {
				log.Error(nil, "DeleteEvent received with no metadata", "DeleteEvent", evt)
				return
			}

			clustersMap.DeleteEvent(evt)
		},
	})
	if err != nil {
		return err
	}

	events := make(chan event.GenericEvent)
	err = c.Watch(
		&source.Channel{Source: events},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}

	// TODO: add a stop channel
	go func() {
		for {
			select {
			// case <-stop:
			// 	return
			case <-time.After(reconcilePeriod):
				// write all clusters to envents chan to be processed
				clustersMap.lock.RLock()
				for _, clEvent := range clustersMap.Map {
					events <- clEvent
				}
				clustersMap.lock.RUnlock()
			}
		}
	}()

	return nil
}

var _ reconcile.Reconciler = &ReconcileMysqlCluster{}

// ReconcileMysqlCluster reconciles a MysqlCluster object
type ReconcileMysqlCluster struct {
	client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// Reconcile reconcile for each mysql cluster state from orchestrator to
// cluster, this reconcile is triggered in a loop.
//
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
// +kubebuilder:rbac:groups=apps,resources=statefulset,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pod,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mysql.presslabs.org,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileMysqlCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the MysqlCluster instance
	cluster := &mysqlv1alpha1.MysqlCluster{}
	err := r.Get(context.TODO(), request.NamespacedName, cluster)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	log.Info(fmt.Sprintf("Reconciling cluster: %s/%s", cluster.Name, cluster.Namespace))

	// TODO: continue...
	return reconcile.Result{}, nil
}
