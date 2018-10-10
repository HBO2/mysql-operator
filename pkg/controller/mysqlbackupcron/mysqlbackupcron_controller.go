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

package mysqlbackupcron

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/wgliang/cron"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	mysqlv1alpha1 "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
	"github.com/presslabs/mysql-operator/pkg/options"
)

const (
	controllerName = "mysqlbackupcron-controller"
)

var log = logf.Log.WithName(controllerName)

// Add creates a new MysqlBackup Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	sscron := startStopCron{}
	err := mgr.Add(sscron)
	if err != nil {
		return err
	}
	return add(mgr, newReconciler(mgr, sscron.Cron))
}

type startStopCron struct {
	Cron *cron.Cron
}

func (c startStopCron) Start(stop <-chan struct{}) error {
	c.Cron.Start()
	<-stop
	c.Cron.Stop()

	return nil
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, cron *cron.Cron) reconcile.Reconciler {
	return &ReconcileMysqlBackup{
		Client:   mgr.GetClient(),
		scheme:   mgr.GetScheme(),
		recorder: mgr.GetRecorder(controllerName),
		opt:      options.GetOptions(),
		cron:     cron,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	enqueuer := handler.EnqueueRequestForObject{}
	// Watch for changes to MysqlCluster. just for add and delete events
	err = c.Watch(&source.Kind{Type: &mysqlv1alpha1.MysqlCluster{}}, &handler.Funcs{
		CreateFunc: enqueuer.Create,
		DeleteFunc: enqueuer.Delete,
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileMysqlBackup{}

// ReconcileMysqlBackup reconciles a MysqlBackup object
type ReconcileMysqlBackup struct {
	client.Client
	scheme          *runtime.Scheme
	recorder        record.EventRecorder
	opt             *options.Options
	cron            *cron.Cron
	lockJobRegister sync.Mutex
}

// Reconcile reads that state of the cluster for a MysqlBackup object and makes changes based on the state read
// and what is in the MysqlBackup.Spec
func (r *ReconcileMysqlBackup) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the MysqlBackup instance
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

	// if spec.backupScheduler is not set then don't do anything
	if len(cluster.Spec.BackupSchedule) == 0 {
		return reconcile.Result{}, nil
	}

	schedule, err := cron.Parse(cluster.Spec.BackupSchedule)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to parse schedule: %s", err)
	}

	log.V(1).Info("register cluster in cronjob", "cluster", cluster, "schedule", schedule)

	return reconcile.Result{}, r.registerCluster(cluster, schedule)
}

func (r *ReconcileMysqlBackup) registerCluster(cluster *mysqlv1alpha1.MysqlCluster, schedule cron.Schedule) error {
	r.lockJobRegister.Lock()
	defer r.lockJobRegister.Unlock()

	for _, entry := range r.cron.Entries() {
		j, ok := entry.Job.(job)
		if ok && j.Name == cluster.Name && j.Namespace == cluster.Namespace {
			log.V(1).Info("cluster already added to cron.", "cluster", cluster.Name)

			// change scheduler for already added crons
			if !reflect.DeepEqual(entry.Schedule, schedule) {
				log.Info("update cluster scheduler", "cluster",
					cluster.Name, "scheduler", cluster.Spec.BackupSchedule)

				r.cron.Remove(cluster.Name)
				break
			}

			// update backups limit for already added crons
			if !reflect.DeepEqual(cluster.Spec.BackupScheduleJobsHistoryLimit, j.BackupScheduleJobsHistoryLimit) {
				newValFmt := "inf"
				if cluster.Spec.BackupScheduleJobsHistoryLimit != nil {
					newValFmt = fmt.Sprintf("%d", cluster.Spec.BackupScheduleJobsHistoryLimit)
				}
				log.Info("update cluster backup limit", "cluster",
					cluster.Name, "limit_val", newValFmt)
				r.cron.Remove(cluster.Name)
				break

			}

			// nothing to change for this cluster, return
			return nil
		}
	}

	r.cron.Schedule(schedule, job{
		Name:                           cluster.Name,
		Namespace:                      cluster.Namespace,
		Client:                         r,
		BackupRunning:                  new(bool),
		lock:                           new(sync.Mutex),
		BackupScheduleJobsHistoryLimit: cluster.Spec.BackupScheduleJobsHistoryLimit,
	}, cluster.Name)

	return nil
}
