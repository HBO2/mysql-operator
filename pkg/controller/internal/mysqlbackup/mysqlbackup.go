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

package mysqlbackup

import (
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	api "github.com/presslabs/mysql-operator/pkg/apis/mysql/v1alpha1"
)

var log = logf.Log.WithName("update-status")

// Wrapper is a type wrapper over MysqlBackup that contains the Business logic
type Wrapper struct {
	*api.MysqlBackup
}

// New returns a wraper object over MysqlBackup
func New(backup *api.MysqlBackup) *Wrapper {
	return &Wrapper{
		MysqlBackup: backup,
	}
}
