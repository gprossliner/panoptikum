/*
Copyright 2026.

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

// Package apicall wraps controller-runtime client calls with xhdl.Throw
// error handling and V(3) debug logging, so reconcilers don't repeat
// if err != nil { return ..., err } after every call.
package apicall

import (
	"fmt"
	"reflect"

	"github.com/gprossliner/xhdl"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// ApiGet performs a Get and throws on any error other than success.
func ApiGet(ctx xhdl.Context, cl client.Client, key client.ObjectKey, obj client.Object) {
	log := ctrllog.FromContext(ctx)
	ctx.Throw(cl.Get(ctx, key, obj))
	log.V(3).Info(fmt.Sprintf("API: Get kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}

// ApiRefresh re-fetches obj in place by its own name/namespace.
func ApiRefresh(ctx xhdl.Context, cl client.Client, obj client.Object) {
	log := ctrllog.FromContext(ctx)
	ctx.Throw(cl.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj))
	log.V(3).Info(fmt.Sprintf("API: Get kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}

// ApiTryGet tried to get an object. It returns false if the object has not been found (404)
// If it has not been found it initializes the object with the name / namespace so that a new corresponding object can be created
// It initializes .Data from ConfigMaps / Secrets to create a new object
func ApiTryGet(ctx xhdl.Context, cl client.Client, key client.ObjectKey, obj client.Object) (found bool) {
	log := ctrllog.FromContext(ctx)

	err := cl.Get(ctx, key, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			found = false

			// initializes the object with the name / namespace so that
			// the operator can use it to create a new object
			obj.SetName(key.Name)
			obj.SetNamespace(key.Namespace)
		} else {
			ctx.Throw(err)
		}
	} else {
		found = true
	}

	// init .Data for Secret
	if secret, ok := obj.(*v1.Secret); ok {
		// Perform operations on the secret object
		// For example, initialize the Data field if necessary
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
	}

	// init .Data for ConfigMap
	if configMap, ok := obj.(*v1.ConfigMap); ok {
		// Perform operations on the secret object
		// For example, initialize the Data field if necessary
		if configMap.Data == nil {
			configMap.Data = make(map[string]string)
		}
	}

	log.V(3).Info(fmt.Sprintf("API: Get kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
	return found
}

func ApiUpdate(ctx xhdl.Context, cl client.Client, obj client.Object) {
	log := ctrllog.FromContext(ctx)

	log.V(3).Info(fmt.Sprintf("API: Updating Object kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
	ctx.Throw(cl.Update(ctx, obj))
	log.V(3).Info(fmt.Sprintf("API: Object updated kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}

func ApiUpdateStatus(ctx xhdl.Context, cl client.Client, obj client.Object) {
	log := ctrllog.FromContext(ctx)

	log.V(3).Info(fmt.Sprintf("API: Updating Status kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
	ctx.Throw(cl.Status().Update(ctx, obj))
	log.V(3).Info(fmt.Sprintf("API: Status updated kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}

func ApiList(ctx xhdl.Context, cl client.Client, list client.ObjectList, opts ...client.ListOption) {
	log := ctrllog.FromContext(ctx)

	log.V(3).Info(fmt.Sprintf("API: List kind:%s", list.GetObjectKind().GroupVersionKind().Kind))
	ctx.Throw(cl.List(ctx, list, opts...))

	if logV := log.V(3); logV.Enabled() {
		v := reflect.ValueOf(list).Elem()
		vitems := v.FieldByName("Items")

		if vitems.Kind() != reflect.Slice {
			panic("expected slice type, found " + vitems.Kind().String())
		}

		for i := 0; i < vitems.Len(); i++ {
			item := vitems.Index(i).FieldByName("ObjectMeta").Interface()
			obj := item.(metav1.ObjectMeta)
			logV.Info(fmt.Sprintf("API: List Element namespace:%s name:%s ResourceVersion=%s", obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
		}
	}
}

func ApiCreate(ctx xhdl.Context, cl client.Client, obj client.Object) {
	log := ctrllog.FromContext(ctx)
	log.V(3).Info(fmt.Sprintf("API: Creating object namespace:%s name:%s", obj.GetNamespace(), obj.GetName()))
	ctx.Throw(cl.Create(ctx, obj))
	log.V(3).Info(fmt.Sprintf("API: Created object kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}

func ApiDelete(ctx xhdl.Context, cl client.Client, obj client.Object) {
	log := ctrllog.FromContext(ctx)
	log.V(3).Info(fmt.Sprintf("API: Deleting object namespace:%s name:%s", obj.GetNamespace(), obj.GetName()))
	ctx.Throw(cl.Delete(ctx, obj))
	log.V(3).Info(fmt.Sprintf("API: Deleted object kind:%s namespace:%s name:%s OK, ResourceVersion=%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), obj.GetResourceVersion()))
}
