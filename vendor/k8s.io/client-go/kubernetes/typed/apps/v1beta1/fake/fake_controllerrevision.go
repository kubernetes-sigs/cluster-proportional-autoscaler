/*
Copyright The Kubernetes Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1beta1 "k8s.io/api/apps/v1beta1"
	appsv1beta1 "k8s.io/client-go/applyconfigurations/apps/v1beta1"
	gentype "k8s.io/client-go/gentype"
	typedappsv1beta1 "k8s.io/client-go/kubernetes/typed/apps/v1beta1"
)

// fakeControllerRevisions implements ControllerRevisionInterface
type fakeControllerRevisions struct {
	*gentype.FakeClientWithListAndApply[*v1beta1.ControllerRevision, *v1beta1.ControllerRevisionList, *appsv1beta1.ControllerRevisionApplyConfiguration]
	Fake *FakeAppsV1beta1
}

func newFakeControllerRevisions(fake *FakeAppsV1beta1, namespace string) typedappsv1beta1.ControllerRevisionInterface {
	return &fakeControllerRevisions{
		gentype.NewFakeClientWithListAndApply[*v1beta1.ControllerRevision, *v1beta1.ControllerRevisionList, *appsv1beta1.ControllerRevisionApplyConfiguration](
			fake.Fake,
			namespace,
			v1beta1.SchemeGroupVersion.WithResource("controllerrevisions"),
			v1beta1.SchemeGroupVersion.WithKind("ControllerRevision"),
			func() *v1beta1.ControllerRevision { return &v1beta1.ControllerRevision{} },
			func() *v1beta1.ControllerRevisionList { return &v1beta1.ControllerRevisionList{} },
			func(dst, src *v1beta1.ControllerRevisionList) { dst.ListMeta = src.ListMeta },
			func(list *v1beta1.ControllerRevisionList) []*v1beta1.ControllerRevision {
				return gentype.ToPointerSlice(list.Items)
			},
			func(list *v1beta1.ControllerRevisionList, items []*v1beta1.ControllerRevision) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}