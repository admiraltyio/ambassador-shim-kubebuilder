/*
Copyright 2018 Admiralty Technologies Inc.

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

package mapping

import (
	"context"
	"reflect"

	ambassadorshimv1alpha1 "admiralty.io/ambassador-shim-kubebuilder/pkg/apis/ambassadorshim/v1alpha1"
	yaml "gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Add creates a new Mapping Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMapping{Client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	c, err := controller.New("mapping-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &ambassadorshimv1alpha1.Mapping{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &ambassadorshimv1alpha1.Mapping{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileMapping{}

// ReconcileMapping reconciles a Mapping object
type ReconcileMapping struct {
	client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Mapping object and makes changes based on the state read
// and what is in the Mapping.Spec
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ambassadorshim.admiralty.io,resources=mappings,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileMapping) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	m := &ambassadorshimv1alpha1.Mapping{}
	if err := r.Get(context.TODO(), request.NamespacedName, m); err != nil {
		if errors.IsNotFound(err) {
			// The Mapping was deleted:
			// garbage collection will take care of the dummy Service.
			return reconcile.Result{}, nil
		}
		// Something actually went wrong.
		return reconcile.Result{}, err
	}

	// generate the desired Service from the Mapping
	ds, err := dummyService(m)
	if err != nil {
		return reconcile.Result{}, err
	}
	if err := controllerutil.SetControllerReference(m, ds, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// get the observed Service, if any
	os := &corev1.Service{}
	if err := r.Get(context.TODO(), types.NamespacedName{Name: ds.Name, Namespace: ds.Namespace}, os); err != nil {
		// if the Service doesn't exist, create it
		// (update Mapping status for current observed state)
		if errors.IsNotFound(err) {
			m.Status = ambassadorshimv1alpha1.MappingStatus{
				Configured: false,
				UpToDate:   false,
			}
			err := r.Update(context.TODO(), m)
			if err != nil {
				return reconcile.Result{}, err
			}

			err = r.Create(context.TODO(), ds)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	// if the Service exist and its annotation matches the MappingSpec
	// do nothing but update the Mapping status
	if reflect.DeepEqual(ds.Annotations, os.Annotations) {
		m.Status = ambassadorshimv1alpha1.MappingStatus{
			Configured: true,
			UpToDate:   true,
		}
		err := r.Update(context.TODO(), m)
		return reconcile.Result{}, err
	}

	// if the Service exists but its annotation doesn't match
	// update it accordingly
	m.Status = ambassadorshimv1alpha1.MappingStatus{
		Configured: true,
		UpToDate:   false,
	}
	if err := r.Update(context.TODO(), m); err != nil {
		return reconcile.Result{}, err
	}

	os.Annotations = ds.Annotations
	err = r.Update(context.TODO(), os)
	return reconcile.Result{}, err
}

type LegacyMapping struct {
	ApiVersion string
	Kind       string
	Name       string
	Prefix     string
	Service    string
}

func dummyService(m *ambassadorshimv1alpha1.Mapping) (*corev1.Service, error) {
	// Let's build the annotation as a struct,
	// before marshalling it to YAML.
	lm := LegacyMapping{
		ApiVersion: "ambassador/v0",
		Kind:       "Mapping",
		Name:       m.Name,
		Prefix:     m.Spec.Prefix,
		Service:    m.Spec.Service,
	}

	y, err := yaml.Marshal(&lm)
	if err != nil {
		return nil, err
	}

	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-ambassadorshim",
			Namespace: m.Namespace,
			// OwnerReferences: []metav1.OwnerReference{
			// 	*metav1.NewControllerRef(m, m.GroupVersionKind()),
			// },
			Annotations: map[string]string{
				"getambassador.io/config": string(y),
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				corev1.ServicePort{Port: 80},
			}, // dummy port (required in ServiceSpec)
		},
	}

	return s, nil
}
