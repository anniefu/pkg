/*
Copyright 2019 The Knative Authors

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

package metrics

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// observer is the signature of the callbacks that notify an observer of the latest
// state of a particular configuration.  An observer should not modify the provided
// ConfigMap, and should `.DeepCopy()` it for persistence (or otherwise process its
// contents).
type observer struct {
	OnAdd    func(*corev1.Secret)
	OnUpdate func(*corev1.Secret, *corev1.Secret)
	OnDelete func(*corev1.Secret)
}

// singleSecretWatcher defines the interface that a configmap implementation must implement.
type singleSecretWatcher interface {
	// Watch is called to register callbacks to be notified when a named ConfigMap changes.
	StartWatch() error

	// Start is called to initiate the watches and provide a channel to signal when we should
	// stop watching.  When Start returns, all registered Observers will be called with the
	// initial state of the ConfigMaps they are watching.
	StopWatch()

	GetSecret() (*corev1.Secret, error)
}

func newSingleSecretWatcher(namespace string, name string, observers ...observer) (singleSecretWatcher, error) {
	impl := &singleSecretWatcherImpl{
		SecretNamespace: namespace,
		SecretName:      name,
		observers:       observers,
	}

	if clientErr := impl.setupKubeclient(); clientErr != nil {
		return nil, clientErr
	}

	impl.setupInformer()
	return impl, nil
}

// stackdriverSecretWatcher watches a single secret
type singleSecretWatcherImpl struct {
	// kubeclient is the in-cluster Kubernetes kubeclient, which is lazy-initialized on first use.
	kubeclient *kubernetes.Clientset

	// secretWatcherInformer watches the Stackdriver secret if useStackdriverSecretEnabled is true.
	secretWatcherInformer cache.SharedIndexInformer
	// secretWatcherStopCh stops the secretWatcherInformer when required.
	secretWatcherStopCh chan struct{}

	// SecretName is the name of the secret being watched.
	SecretName string
	// SecretNamespace is the namespace of the secret being watched.
	SecretNamespace string
	// observers is the list of observers to trigger when the Secret is changed.
	observers []observer
}

func (s *singleSecretWatcherImpl) StartWatch() error {
	go s.secretWatcherInformer.Run(s.secretWatcherStopCh)
	return nil
}

func (s *singleSecretWatcherImpl) StopWatch() {
	<-s.secretWatcherStopCh
}

func (s *singleSecretWatcherImpl) GetSecret() (*corev1.Secret, error) {
	sec, secErr := s.kubeclient.CoreV1().Secrets(s.SecretNamespace).Get(s.SecretName, metav1.GetOptions{})

	if secErr != nil {
		return nil, fmt.Errorf("Error getting Secret [%v] in namespace [%v]: %v", s.SecretName, s.SecretNamespace, secErr)
	}

	return sec, nil
}

func (s *singleSecretWatcherImpl) setupInformer() {
	informers := informers.NewSharedInformerFactoryWithOptions(s.kubeclient, 0)
	s.secretWatcherInformer = informers.Core().V1().Secrets().Informer()

	s.secretWatcherInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			sec := obj.(corev1.Secret)
			for _, obs := range s.observers {
				obs.OnAdd(&sec)
			}
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			sOld := oldObj.(corev1.Secret)
			sNew := newObj.(corev1.Secret)

			for _, obs := range s.observers {
				obs.OnUpdate(&sOld, &sNew)
			}
		},
		DeleteFunc: func(obj interface{}) {
			sec := obj.(corev1.Secret)
			for _, obs := range s.observers {
				obs.OnDelete(&sec)
			}
		},
	})
}

// setupKubeclient is the lazy initializer for kubeclient.
func (s *singleSecretWatcherImpl) setupKubeclient() error {
	config, configErr := rest.InClusterConfig()
	if configErr != nil {
		return configErr
	}

	cs, clientErr := kubernetes.NewForConfig(config)
	if clientErr != nil {
		return clientErr
	}

	s.kubeclient = cs
	return nil
}
