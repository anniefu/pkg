package metrics

import (
	"fmt"
	"testing"
	"time"

	// fcache "k8s.io/client-go/tools/cache/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	fclient "k8s.io/client-go/kubernetes/fake"
)

// secretForTest encapsulates Secret metadata and a Secret.
type secretForTest struct {
	namespace string
	name      string
	dataKey   string
	dataValue string
	secret    corev1.Secret
}

// newSecretForTest constructs a secretForTest.
func newSecretForTest(namespace string, name string, dataKey string, dataValue string) *secretForTest {
	return &secretForTest{
		namespace: namespace,
		name:      name,
		dataKey:   dataKey,
		dataValue: dataValue,
		secret: corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "apps/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Data: map[string][]byte{
				dataKey: []byte(dataValue),
			},
			Type: "Opaque",
		},
	}
}

var (
	// kubeclientForTest is a fake kubeclient that can be use for testing.
	// This should be passed to any functionality being tested that require a kubeclient.
	kubeclientForTest kubernetes.Interface

	// dsft is a default secret for tests, for convenience.
	dsft = newSecretForTest("test", "test-secret", "key.json", "token")
	// defaultNumTestObservers is the default number of observer callbacks for tests.
	defaultNumTestObservers = 2

	// defaultSecretWatchersToTestConstructors is a convenience list of constructors of secret watchers that should be tested.
	defaultSecretWatchersToTestConstructors = []func(t *testing.T, obs ...Observer) SecretWatcher{
		func(t *testing.T, obs ...Observer) SecretWatcher {
			return mustNewSecretWatcher(t, obs...)
		},
		func(t *testing.T, obs ...Observer) SecretWatcher {
			return mustNewSecretWatcherSingleNamespace(t, dsft.namespace, obs...)
		},
		func(t *testing.T, obs ...Observer) SecretWatcher {
			return mustNewSecretWatcherSingleSecret(t, dsft.namespace, dsft.name, obs...)
		},
	}
)

// testSetup sets up tests.
func testSetup() {
	// Reset kubeclient on every test to clear out state
	kubeclientForTest = fclient.NewSimpleClientset()
	// Ensure test and secret watcher share the same kubeclient
	createStackdriverKubeclientFunc = func() (kubernetes.Interface, error) {
		return kubeclientForTest, nil
	}

	dsft = newSecretForTest("test", "test-secret", "key.json", "token")
}

// testCleanup cleans up tests.
func testCleanup() {
	createStackdriverKubeclientFunc = createKubeclient
}

func TestNewSingleSecretWatcher(t *testing.T) {
	testSetup()
	defer testCleanup()

	o := &observerFuncs{
		AddFunc: func(s *corev1.Secret) {
		},
		UpdateFunc: func(sOld *corev1.Secret, sNew *corev1.Secret) {
		},
		DeleteFunc: func(s *corev1.Secret) {
		},
	}

	mustNewSecretWatcherSingleSecret(t, dsft.namespace, dsft.name, o)
}

func TestStartStopWatch(t *testing.T) {
	testSetup()
	defer testCleanup()

	added := false
	o := &observerFuncs{
		AddFunc: func(s *corev1.Secret) {
			added = true
		},
	}

	watcher := mustNewSecretWatcherSingleSecret(t, dsft.namespace, dsft.name, o)

	// Test weird patterns of stop/start
	watcher.StopWatch()
	watcher.StartWatch()
	watcher.StartWatch()
	watcher.StopWatch()
	watcher.StopWatch()

	watcher.StartWatch()
	defer watcher.StopWatch()

	kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
	waitForCondition(t, func() bool {
		return added
	}, 3)
}

func TestSecretWatcherGetSecret(t *testing.T) {
	for idx, constructor := range defaultSecretWatchersToTestConstructors {
		testSetup()

		watcher := constructor(t)
		if _, err := watcher.GetSecret(dsft.namespace, dsft.name); err == nil {
			t.Errorf("Expected GetSecret() for Secret %v/%v to fail because secret doesn't exist yet. Watcher at idx: %d", dsft.namespace, dsft.name, idx)
		}

		kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)

		waitForCondition(t, func() bool {
			sec, err := watcher.GetSecret(dsft.namespace, dsft.name)
			if err != nil {
				return false
			}
			assertSecretValue(t, sec, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
			return true
		}, 3)

		testCleanup()
	}
}

func TestSecretWatcherGetSecretInvalidInputs(t *testing.T) {
	testSetup()
	defer testCleanup()

	var invalidCases = []struct {
		name            string
		secretNamespace string
		secretName      string
	}{
		{
			name:            "EmptyNamespace",
			secretNamespace: "",
			secretName:      dsft.name,
		},
		{
			name:            "EmptyName",
			secretNamespace: dsft.namespace,
			secretName:      "",
		},
		{
			name:            "BothEmpty",
			secretNamespace: "",
			secretName:      "",
		},
	}

	for _, test := range invalidCases {
		for idx, constructor := range defaultSecretWatchersToTestConstructors {
			watcher := constructor(t)
			if _, err := watcher.GetSecret(test.secretNamespace, test.secretName); err == nil {
				t.Errorf("Calls to GetSecret() should fail if namespace or name are invalid; namespace: %v, name: %v. Watcher at idx: %d", test.secretNamespace, test.secretName, idx)
			}
		}
	}
}

func XTestDifferentSecretWatcherScopes(t *testing.T) {
	testSetup()
	defer testCleanup()

	obs := &observerFuncs{
		AddFunc: func(s *corev1.Secret) {},
	}

	testObsAllSecrets := newTestObserver(obs)
	testObsSingleNamespace := newTestObserver(obs)
	testObsSingleSecret := newTestObserver(obs)

	namespaceA := "a"
	namespaceB := "b"
	nonDefaultName := "non-default-secret"

	// These secrets will be created in the order they are declared
	triggerAllSecretWatcher := newSecretForTest(namespaceB, dsft.name, dsft.dataKey, dsft.dataValue)
	triggerSingleNamespaceWatcher := newSecretForTest(namespaceA, dsft.name, dsft.dataKey, dsft.dataValue)
	triggerSingleSecretWatcher := newSecretForTest(namespaceA, nonDefaultName, dsft.dataKey, dsft.dataValue)

	watcherAllSecrets := mustNewSecretWatcher(t, testObsAllSecrets)
	watcherAllSecrets.StartWatch()
	defer watcherAllSecrets.StopWatch()

	watcherSingleNamespace := mustNewSecretWatcherSingleNamespace(t, triggerSingleNamespaceWatcher.namespace, testObsSingleNamespace)
	watcherSingleNamespace.StartWatch()
	defer watcherSingleNamespace.StopWatch()

	watcherSingleSecret := mustNewSecretWatcherSingleSecret(t, triggerSingleSecretWatcher.namespace, triggerSingleSecretWatcher.name, testObsSingleSecret)
	watcherSingleSecret.StartWatch()
	defer watcherSingleSecret.StopWatch()

	// First, trigger just the secret watcher for all secrets
	kubeclientForTest.CoreV1().Secrets(triggerAllSecretWatcher.namespace).Create(&triggerAllSecretWatcher.secret)
	waitForCondition(t, testObsAllSecrets.AllCallbacksCalled, 3)

	if testObsSingleNamespace.AllCallbacksCalled() {
		t.Errorf("Updates to a Secret in namespace [%v] should not have triggered a single-namespace secret watcher watching namespace [%v]", namespaceB, watcherSingleNamespace.GetNamespaceWatched())
	}

	if testObsSingleSecret.AllCallbacksCalled() {
		t.Errorf("Updates to a Secret in namespace [%v] should not have triggered a single-secret secret watcher watching for Secret [%v/%v]", namespaceB, watcherSingleSecret.GetNamespaceWatched(), watcherSingleSecret.GetNameWatched())
	}

	// Second, trigger just the secret watcher for single namespace
	kubeclientForTest.CoreV1().Secrets(triggerSingleNamespaceWatcher.namespace).Create(&triggerSingleNamespaceWatcher.secret)
	waitForCondition(t, testObsSingleNamespace.AllCallbacksCalled, 3)

	if testObsSingleSecret.AllCallbacksCalled() {
		t.Errorf("Updates to Secret [%v/%v] should not have triggered a single-secret secret watcher watching for Secret [%v/%v]", namespaceA, nonDefaultName, watcherSingleSecret.GetNamespaceWatched(), watcherSingleSecret.GetNameWatched())
	}

	// Third, trigger the secret watcher for single secret
	kubeclientForTest.CoreV1().Secrets(triggerSingleSecretWatcher.namespace).Create(&triggerSingleSecretWatcher.secret)
	waitForCondition(t, testObsSingleSecret.AllCallbacksCalled, 3)

	secretList, _ := kubeclientForTest.CoreV1().Secrets(triggerSingleSecretWatcher.namespace).List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%v", "non-default-secret"),
	})
	for _, s := range secretList.Items {
		t.Logf("%v", s.ObjectMeta)
	}
}

// testObserver is an implementation of the the secretWatcher observer interface
// that also holds state about whether the observer's callbacks have been triggered.
type testObserver struct {
	oFuncs         *observerFuncs
	onAddCalled    bool
	onUpdateCalled bool
	onDeleteCalled bool
}

// newTestObserver constructs a testObserver from observerFuncs.
func newTestObserver(inputObsFuncs *observerFuncs) *testObserver {
	// If a callback is nil, consider it to be called by default.
	return &testObserver{
		oFuncs:         inputObsFuncs,
		onAddCalled:    inputObsFuncs.AddFunc == nil,
		onUpdateCalled: inputObsFuncs.UpdateFunc == nil,
		onDeleteCalled: inputObsFuncs.DeleteFunc == nil,
	}
}

func (tObs *testObserver) OnAdd(s *corev1.Secret) {
	tObs.oFuncs.OnAdd(s)
	tObs.onAddCalled = true
}

func (tObs *testObserver) OnUpdate(sOld *corev1.Secret, sNew *corev1.Secret) {
	tObs.oFuncs.OnUpdate(sOld, sNew)
	tObs.onUpdateCalled = true
}

func (tObs *testObserver) OnDelete(s *corev1.Secret) {
	tObs.oFuncs.OnDelete(s)
	tObs.onDeleteCalled = true
}

func (tObs *testObserver) AllCallbacksCalled() bool {
	return tObs.onAddCalled && tObs.onUpdateCalled && tObs.onDeleteCalled
}

func TestSecretWatcherCallbacks(t *testing.T) {
	var testCases = []struct {
		name                          string
		obs                           []observerFuncs
		secretChangesBeforeStartWatch func(kubernetes.Interface) // kubeclient operations executed befores secret watcher watch started
		secretChangesAfterStartWatch  func(kubernetes.Interface) // kubeclient operations executed after secret watcher watch started
	}{
		{
			name: "OnDelete",
			obs: []observerFuncs{
				// setup multiple observers
				observerFuncs{
					DeleteFunc: func(s *corev1.Secret) {
					},
				},
				observerFuncs{
					DeleteFunc: func(s *corev1.Secret) {
					},
				},
			},
			secretChangesBeforeStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
			},
			secretChangesAfterStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclient.CoreV1().Secrets(dsft.namespace).Delete(dsft.name, &metav1.DeleteOptions{})
			},
		},
		{
			name: "OnAdd",
			obs: []observerFuncs{
				// setup multiple observers
				observerFuncs{
					AddFunc: func(s *corev1.Secret) {
						assertSecretValue(t, s, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
					},
				},
				observerFuncs{
					AddFunc: func(s *corev1.Secret) {
						assertSecretValue(t, s, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
					},
				},
			},
			secretChangesAfterStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
			},
		},
		{
			name: "OnAddSecretAlreadyExists",
			obs: []observerFuncs{
				// setup multiple observers
				observerFuncs{
					AddFunc: func(s *corev1.Secret) {
						assertSecretValue(t, s, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
					},
				},
				observerFuncs{
					AddFunc: func(s *corev1.Secret) {
						assertSecretValue(t, s, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
					},
				},
			},
			secretChangesBeforeStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
			},
			secretChangesAfterStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
			},
		},
		{
			name: "OnUpdate",
			obs: []observerFuncs{
				// setup multiple observers
				observerFuncs{
					UpdateFunc: func(sOld *corev1.Secret, sNew *corev1.Secret) {
						assertSecretValue(t, sOld, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
						assertSecretValue(t, sNew, dsft.namespace, dsft.name, dsft.dataKey, "newToken")
					},
				},
				observerFuncs{
					UpdateFunc: func(sOld *corev1.Secret, sNew *corev1.Secret) {
						assertSecretValue(t, sOld, dsft.namespace, dsft.name, dsft.dataKey, dsft.dataValue)
						assertSecretValue(t, sNew, dsft.namespace, dsft.name, dsft.dataKey, "newToken")
					},
				},
			},
			secretChangesBeforeStartWatch: func(kubeclient kubernetes.Interface) {
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Create(&dsft.secret)
			},
			secretChangesAfterStartWatch: func(kubeclient kubernetes.Interface) {
				// Modify secret value and update
				dsft.secret.Data[dsft.dataKey] = []byte("newToken")
				kubeclientForTest.CoreV1().Secrets(dsft.namespace).Update(&dsft.secret)
			},
		},
	}

	for _, test := range testCases {
		testSetup()

		// Setup sets of testObservers for each secret watcher to test
		numSecretWatcherTypes := len(defaultSecretWatchersToTestConstructors)
		observerSets := make([][]Observer, numSecretWatcherTypes)
		for i := 0; i < numSecretWatcherTypes; i++ {
			observerSets[i] = make([]Observer, len(test.obs))
			for j, o := range test.obs {
				observerSets[i][j] = newTestObserver(&o)
			}
		}

		if test.secretChangesBeforeStartWatch != nil {
			test.secretChangesBeforeStartWatch(kubeclientForTest)
		}

		// Start a secret watcher of each type.
		for i := 0; i < numSecretWatcherTypes; i++ {
			watcher := defaultSecretWatchersToTestConstructors[i](t, observerSets[i]...)
			watcher.StartWatch()
			defer watcher.StopWatch()
		}

		if test.secretChangesAfterStartWatch != nil {
			test.secretChangesAfterStartWatch(kubeclientForTest)
		}

		// There may be a slight delay between when the secret is changed and when the
		// watcher is notified, so wait up to a few seconds.
		waitForCondition(t, func() bool {
			for _, obs := range observerSets {
				for _, o := range obs {
					if o.(*testObserver).AllCallbacksCalled() {
						return false
					}
				}
			}
			return true
		}, 3)

		testCleanup()
	}
}

func mustNewSecretWatcher(t *testing.T, observers ...Observer) SecretWatcher {
	return mustNewSecretWatcherHelper(t, func() (SecretWatcher, error) { return NewSecretWatcher(observers...) })
}

func mustNewSecretWatcherSingleNamespace(t *testing.T, namespace string, observers ...Observer) SecretWatcher {
	return mustNewSecretWatcherHelper(t, func() (SecretWatcher, error) { return NewSecretWatcherSingleNamespace(namespace, observers...) })
}

func mustNewSecretWatcherSingleSecret(t *testing.T, namespace string, name string, observers ...Observer) SecretWatcher {
	return mustNewSecretWatcherHelper(t, func() (SecretWatcher, error) { return NewSecretWatcherSingleSecret(namespace, name, observers...) })
}

func mustNewSecretWatcherHelper(t *testing.T, constructor func() (SecretWatcher, error)) SecretWatcher {
	watcher, err := constructor()
	if err != nil {
		t.Errorf("Failed to create secret watcher: %v", err)
	}

	return watcher
}

func waitForCondition(t *testing.T, condition func() bool, timeoutSec int) {
	for i := 0; i < timeoutSec+1; i++ {
		if condition() {
			return
		}

		time.Sleep(time.Second * 1)
	}

	t.Errorf("Timed out waiting for condition to become true, checked every second for %d seconds", timeoutSec)
}

func assertSecretValue(t *testing.T, secret *corev1.Secret, namespace string, name string, expectedDataKey string, expectedDataValue string) {
	if val, ok := secret.Data[expectedDataKey]; !ok {
		t.Errorf("Retrieved incorrect Secret data for Secret %v/%v from secret watcher. Expected data to contain field '%v'. Got secret data: %v", namespace, name, expectedDataKey, secret.Data)
	} else if string(val) != expectedDataValue {
		t.Errorf("Retrieved incorrect Secret data for Secret %v/%v from secret watcher. Expected field '%v' to have value '%v'. Got secret data: %v", namespace, name, expectedDataKey, []byte(expectedDataValue), secret.Data)
	}
}
