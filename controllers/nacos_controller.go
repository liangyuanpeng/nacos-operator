/*
Copyright 2021.

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

package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-logr/logr"
	"github.com/juju/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/kubectl/pkg/util"
	ctrl "sigs.k8s.io/controller-runtime"

	corev1beta1 "github.com/liangyuanpeng/nacos-operator/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NacosReconciler reconciles a Nacos object
type NacosReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.liangyuanpeng.nacos.io,resources=nacos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.liangyuanpeng.nacos.io,resources=nacos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.liangyuanpeng.nacos.io,resources=nacos/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Nacos object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *NacosReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("nacos", req.NamespacedName)

	log.Info("this.is.req.info", "req.Name", req.Name, "req.Namespace", req.Namespace)

	nacos := &corev1beta1.Nacos{}
	err := r.Get(ctx, req.NamespacedName, nacos)
	if err != nil {
		if isNotFound(err) {

			cm := &corev1.ConfigMap{}
			cmErr := r.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, cm)
			log.Info("select configmap:", "name and namespace", req.Name+"#"+req.Namespace, "error content:", cmErr, "foundSvc", cm)
			if cmErr == nil {
				log.Info("Deleteing a new configmap", "configmap.Namespace", cm.Namespace, "configmap.Name", cm.Name)
				cmErr = r.Delete(ctx, cm)
				if cmErr != nil {
					log.Error(cmErr, "Failed to delete old configmap", "configmap.Namespace", cm.Namespace, "configmap.Name", cm.Name)
					return ctrl.Result{}, cmErr
				}
			}

			foundSvc := &corev1.Service{}
			err = r.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, foundSvc)
			log.Info("select service:", "name and namespace", req.Name+"#"+req.Namespace, "error content:", err, "foundSvc", foundSvc)
			if err == nil {
				log.Info("Deleteing a new Service", "Service.Namespace", foundSvc.Namespace, "Service.Name", foundSvc.Name)
				err = r.Delete(ctx, foundSvc)
				if err != nil {
					log.Error(err, "Failed to delete old Service", "Service.Namespace", foundSvc.Namespace, "Service.Name", foundSvc.Name)
					return ctrl.Result{}, err
				}
			} else {
				log.Info("select service result", "is not found", isNotFound(err))
				return ctrl.Result{}, err
			}

			foundNodeSvc := &corev1.Service{}
			err = r.Get(ctx, types.NamespacedName{Name: req.Name + "node", Namespace: req.Namespace}, foundNodeSvc)
			log.Info("select service:", "name and namespace", req.Name+"node#"+req.Namespace, "error content:", err, "foundSvc", foundNodeSvc)
			if err == nil {
				log.Info("Deleteing a new Service", "Service.Namespace", foundNodeSvc.Namespace, "Service.Name", foundNodeSvc.Name)
				err = r.Delete(ctx, foundNodeSvc)
				if err != nil {
					log.Error(err, "Failed to delete old Service", "Service.Namespace", foundNodeSvc.Namespace, "Service.Name", foundNodeSvc.Name)
					return ctrl.Result{}, err
				}
			} else {
				log.Info("select service result", "is not found", isNotFound(err))
				return ctrl.Result{}, err
			}

			log.Info("nacos resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get nacos")
		return ctrl.Result{}, err
	}

	//create configmap
	cm := &corev1.ConfigMap{}
	cmErr := r.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, cm)
	if cmErr != nil {
		if isNotFound(cmErr) {
			cmmeta := metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			}
			cm.ObjectMeta = cmmeta
			cm.Data = make(map[string]string)
			handleConfigMapFromFileSources(cm, []string{"/application.properties", "/nacos-logback.xml", "/cluster.conf"})

			cmErr = r.Create(ctx, cm)
			if cmErr != nil {
				log.Info("create a new configmap failed", "Configmap.Namespace", cm.Namespace, "Configmap.Name", cm.Name)
			}
		}
	}

	found := &appsv1.StatefulSet{}

	if err = r.Get(ctx, types.NamespacedName{Name: nacos.Name, Namespace: nacos.Namespace}, found); err != nil {
		if isNotFound(err) {
			dep := r.statefulSetForNacos(nacos)
			log.Info("Creating a new StatefulSet1", "StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)
			err = r.Create(ctx, dep)
			foundSvc := &corev1.Service{}
			err = r.Get(ctx, types.NamespacedName{Name: nacos.Name, Namespace: nacos.Namespace}, foundSvc)
			if err != nil {
				if isNotFound(err) {

					svc := r.serviceForNacos(nacos, "None", corev1.ServiceTypeClusterIP)
					log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
					err = r.Create(ctx, svc)
					if err != nil {
						log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", dep.Name)
						return ctrl.Result{}, err
					}

					svc = r.serviceForNacos(nacos, "", corev1.ServiceTypeNodePort)
					log.Info("Creating a new Service2", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
					err = r.Create(ctx, svc)
					if err != nil {
						log.Error(err, "Failed to create new Service2", "Service.Namespace", svc.Namespace, "Service.Name", dep.Name)
						return ctrl.Result{}, err
					}

				}
			} else {
				//TODO check error for already exist
				log.Info("create stateful svc failed!")
				return ctrl.Result{}, err
			}

		}
		if err != nil {
			log.Info("select.found.err", "err string ", err.Error(), "found:", found)
		}
		return ctrl.Result{}, err
	} else {
		// update sts
		// if found.Spec.Replicas != &nacos.Spec.Size {
		// found.Spec.Replicas = &nacos.Spec.Size
		// servers := ""
		// for i := 0; i < int(*found.Spec.Replicas); i++ {
		// 	//pod.svc.ns.svc.cluster.local   cluster.local--> cluster domain
		// 	servers += nacos.Name + "-" + strconv.Itoa(i) + "." + nacos.Name + "." + nacos.Namespace + ".svc.cluster.local" + ":8848 "
		// }

		// newEnv := []corev1.EnvVar{}
		// for _, v := range found.Spec.Template.Spec.Containers[0].Env {
		// 	if v.Name == "NACOS_SERVERS" {
		// 		v.Value = servers
		// 	}
		// 	newEnv = append(newEnv, v)
		// }
		// //TODO we need more graceful for update nacos cluster info
		// found.Spec.Template.Spec.Containers[0].Env = newEnv
		// log.Info("envs now is ", "envs", found.Spec.Template.Spec.Containers[0].Env)
		// err = r.Update(ctx, found)
		// if err != nil {
		// 	log.Error(err, "update stateful failed!")
		// }
		// }
	}
	log.Info("select.found", "found:", found)
	return ctrl.Result{}, nil
}

//check err for not found
func isNotFound(err error) bool {
	return errors.IsNotFound(err) || strings.Contains(err.Error(), "not found")
}

// handleConfigMapFromFileSources adds the specified file source information
// into the provided configMap
func handleConfigMapFromFileSources(configMap *corev1.ConfigMap, fileSources []string) error {
	for _, fileSource := range fileSources {
		fmt.Printf("begin load file:%s", fileSource)
		keyName, filePath, err := util.ParseFileSource(fileSource)
		if err != nil {
			fmt.Println("end load file,have error", err)
			return err
		}
		info, err := os.Stat(filePath)
		if err != nil {
			switch err := err.(type) {
			case *os.PathError:
				return fmt.Errorf("error reading %s: %v", filePath, err.Err)
			default:
				return fmt.Errorf("error reading %s: %v", filePath, err)
			}

		}
		if info.IsDir() {
			if strings.Contains(fileSource, "=") {
				return fmt.Errorf("cannot give a key name for a directory path")
			}
			fileList, err := ioutil.ReadDir(filePath)
			if err != nil {
				return fmt.Errorf("error listing files in %s: %v", filePath, err)
			}
			for _, item := range fileList {
				itemPath := path.Join(filePath, item.Name())
				if item.Mode().IsRegular() {
					keyName = item.Name()
					err = addKeyFromFileToConfigMap(configMap, keyName, itemPath)
					if err != nil {
						return err
					}
				}
			}
		} else {
			if err := addKeyFromFileToConfigMap(configMap, keyName, filePath); err != nil {
				return err
			}

		}
	}
	return nil
}

// addKeyFromFileToConfigMap adds a key with the given name to a ConfigMap, populating
// the value with the content of the given file path, or returns an error.
func addKeyFromFileToConfigMap(configMap *corev1.ConfigMap, keyName, filePath string) error {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}
	if utf8.Valid(data) {
		return addKeyFromLiteralToConfigMap(configMap, keyName, string(data))
	}
	err = validateNewConfigMap(configMap, keyName)
	if err != nil {
		return err
	}
	configMap.BinaryData[keyName] = data

	return nil
}

// addKeyFromLiteralToConfigMap adds the given key and data to the given config map,
// returning an error if the key is not valid or if the key already exists.
func addKeyFromLiteralToConfigMap(configMap *corev1.ConfigMap, keyName, data string) error {
	err := validateNewConfigMap(configMap, keyName)
	if err != nil {
		return err
	}
	configMap.Data[keyName] = data

	return nil
}

// validateNewConfigMap checks whether the keyname is valid
// Note, the rules for ConfigMap keys are the exact same as the ones for SecretKeys.
func validateNewConfigMap(configMap *corev1.ConfigMap, keyName string) error {
	if errs := validation.IsConfigMapKey(keyName); len(errs) > 0 {
		return fmt.Errorf("%q is not a valid key name for a ConfigMap: %s", keyName, strings.Join(errs, ","))
	}
	if _, exists := configMap.Data[keyName]; exists {
		return fmt.Errorf("cannot add key %q, another key by that name already exists in Data for ConfigMap %q", keyName, configMap.Name)
	}
	if _, exists := configMap.BinaryData[keyName]; exists {
		return fmt.Errorf("cannot add key %q, another key by that name already exists in BinaryData for ConfigMap %q", keyName, configMap.Name)
	}

	return nil
}

func (r *NacosReconciler) serviceForNacos(m *corev1beta1.Nacos, clusterIP string, serviceType corev1.ServiceType) *corev1.Service {
	ls := labelsForNacos(m.Name)
	svcName := m.Name
	if clusterIP == "" {
		svcName = m.Name + "node"
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: m.Namespace,
			Labels:    ls,
		},
		Spec: corev1.ServiceSpec{
			Selector:  ls,
			ClusterIP: clusterIP,
			Type:      serviceType,
			Ports: []corev1.ServicePort{
				{
					Name:     "nacos",
					Port:     8848,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func (r *NacosReconciler) statefulSetForNacos(m *corev1beta1.Nacos) *appsv1.StatefulSet {
	ls := labelsForNacos(m.Name)
	replicas := m.Spec.Size

	servers := ""
	for i := 0; i < int(replicas); i++ {
		//pod.svc.ns.svc.cluster.local   cluster.local-->cluster domain
		servers += m.Name + "-" + strconv.Itoa(i) + "." + m.Name + "." + m.Namespace + ".svc.cluster.local" + ":8848 "
	}
	log.Println("servers:", servers, "ServiceName", m.Name)

	commandLine := `cat /tmp/application.properties > /home/nacos/conf/application.properties
	cat /tmp/nacos-logback.xml > /home/nacos/conf/nacos-logback.xml
	touch /home/nacos/conf/cluster.conf`

	dep := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: m.Name,
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name: "nacos-conf-folder",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
						{
							Name: "cm",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: m.Name,
									},
								},
							},
						}},
					InitContainers: []corev1.Container{
						{
							Name:    "ini",
							Image:   "busybox:1.28.3",
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{commandLine},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "nacos-conf-folder",
									MountPath: "/home/nacos/conf",
								}, {
									Name:      "cm",
									MountPath: "/tmp",
								},
							},
						},
					},
					Containers: []corev1.Container{{
						Image:           "nacos/nacos-server:1.4.0",
						Name:            "nacos",
						ImagePullPolicy: "IfNotPresent",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8848,
							Name:          "nacos",
						}},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "nacos-conf-folder",
								MountPath: "/home/nacos/conf",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name:  "db.num",
								Value: "1",
							},
							{
								Name:  "PREFER_HOST_MODE",
								Value: "hostname",
							},
							// {
							// 	Name:  "EMBEDDED_STORAGE",
							// 	Value: "embedded",
							// },
							{
								Name:  "MYSQL_SERVICE_HOST",
								Value: "mysql",
							},
							{
								Name:  "NACOS_SERVERS",
								Value: servers,
							},
							{
								Name:  "MYSQL_SERVICE_DB_NAME",
								Value: "tester",
							},
							{
								Name:  "MYSQL_SERVICE_PORT",
								Value: "3306",
							},
							{
								Name:  "MYSQL_SERVICE_USER",
								Value: "root",
							},
							{
								Name:  "MYSQL_SERVICE_PASSWORD",
								Value: "123456",
							},
						},
					}, {
						Image:           "peer-finder:0.5",
						Name:            "p",
						ImagePullPolicy: "IfNotPresent",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "nacos-conf-folder",
								MountPath: "/home/nacos/conf",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name:  "CLUSTER_CONF",
								Value: "/home/nacos/conf/cluster.conf",
							},
							// /peer-finder -on-start /start.sh -ns default -service na -on-change /change.sh
						},
						Args: []string{"-on-start", "/start.sh", "-ns", m.Namespace, "-service", m.Name, "-on-change", "/change.sh"},
					}},
				},
			},
		},
	}
	// Set Nacos instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

// labelsForNacos returns the labels for selecting the resources
// belonging to the given Nacos CR name.
func labelsForNacos(name string) map[string]string {
	return map[string]string{"app": "nacos", "release": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// SetupWithManager sets up the controller with the Manager.
func (r *NacosReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1beta1.Nacos{}).Complete(r)
}
