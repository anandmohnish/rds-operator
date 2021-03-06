package dbcluster

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/agill17/rds-operator/pkg/controller/lib"

	agillv1alpha1 "github.com/agill17/rds-operator/pkg/apis/agill/v1alpha1"
	"github.com/aws/aws-sdk-go/service/rds"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_dbcluster")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new DBCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileDBCluster{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("dbcluster-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource DBCluster
	err = c.Watch(&source.Kind{Type: &agillv1alpha1.DBCluster{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner DBCluster
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &agillv1alpha1.DBCluster{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileDBCluster{}

// ReconcileDBCluster reconciles a DBCluster object
type ReconcileDBCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	rdsClient *rds.RDS
}

// Reconcile reads that state of the cluster for a DBCluster object and makes changes based on the state read
// and what is in the DBCluster.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileDBCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {

	instance := &agillv1alpha1.DBCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			logrus.Errorf("Could not find cluster spec: %v", err)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	id := lib.SetDBID(instance.Namespace, instance.Name)
	r.rdsClient = lib.GetRdsClient()

	// get finalizers
	deletionTimeExists := instance.GetDeletionTimestamp() != nil
	currentFinalizers := instance.GetFinalizers()
	anyFinalizersExists := len(currentFinalizers) > 0

	// set finalizers
	if !anyFinalizersExists && !deletionTimeExists {
		finalizersToAdd := append(currentFinalizers, lib.DBClusterFinalizer)
		instance.SetFinalizers(finalizersToAdd)
		err := r.client.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	if deletionTimeExists && anyFinalizersExists {
		// delete cluster
		err := r.deleteCluster(instance, id)
		if err != nil {
			logrus.Errorf("Something went wrong while deleting the dbCluster: %v", err)
			return reconcile.Result{}, err
		}
		// empty out the finalizers list
		instance.SetFinalizers([]string{})
		err = r.client.Update(context.TODO(), instance)
		if err != nil {
			return reconcile.Result{}, err
		}
		// do not reque
		return reconcile.Result{}, nil
	}

	// create
	if !instance.Status.Created {
		logrus.Infof("INIT Create cluster request")
		err := r.createItAndUpdateState(id, request)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// recreateIt will be set by DBInstance whenever needed ( incase dbInstance is trying to reheal and was part of the cluster )
	exists, _ := r.dbClusterExists(id)
	if !exists && instance.Spec.RehealFromLatestSnapshot {
		snapID, _ := lib.GetLatestClusterSnapID(id, instance.Namespace, "us-east-1")
		if snapID != "" {
			logrus.Infof("Recreate cluster requested....")
			r.restoreClusterFromSnap(request, id, snapID)
		}
	}
	return reconcile.Result{Requeue: true, RequeueAfter: 1 * time.Second}, nil
}
