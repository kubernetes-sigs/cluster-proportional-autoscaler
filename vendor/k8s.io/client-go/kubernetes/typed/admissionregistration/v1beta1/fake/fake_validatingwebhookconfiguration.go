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
	v1beta1 "k8s.io/api/admissionregistration/v1beta1"
	admissionregistrationv1beta1 "k8s.io/client-go/applyconfigurations/admissionregistration/v1beta1"
	gentype "k8s.io/client-go/gentype"
	typedadmissionregistrationv1beta1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1beta1"
)

// fakeValidatingWebhookConfigurations implements ValidatingWebhookConfigurationInterface
type fakeValidatingWebhookConfigurations struct {
	*gentype.FakeClientWithListAndApply[*v1beta1.ValidatingWebhookConfiguration, *v1beta1.ValidatingWebhookConfigurationList, *admissionregistrationv1beta1.ValidatingWebhookConfigurationApplyConfiguration]
	Fake *FakeAdmissionregistrationV1beta1
}

func newFakeValidatingWebhookConfigurations(fake *FakeAdmissionregistrationV1beta1) typedadmissionregistrationv1beta1.ValidatingWebhookConfigurationInterface {
	return &fakeValidatingWebhookConfigurations{
		gentype.NewFakeClientWithListAndApply[*v1beta1.ValidatingWebhookConfiguration, *v1beta1.ValidatingWebhookConfigurationList, *admissionregistrationv1beta1.ValidatingWebhookConfigurationApplyConfiguration](
			fake.Fake,
			"",
			v1beta1.SchemeGroupVersion.WithResource("validatingwebhookconfigurations"),
			v1beta1.SchemeGroupVersion.WithKind("ValidatingWebhookConfiguration"),
			func() *v1beta1.ValidatingWebhookConfiguration { return &v1beta1.ValidatingWebhookConfiguration{} },
			func() *v1beta1.ValidatingWebhookConfigurationList {
				return &v1beta1.ValidatingWebhookConfigurationList{}
			},
			func(dst, src *v1beta1.ValidatingWebhookConfigurationList) { dst.ListMeta = src.ListMeta },
			func(list *v1beta1.ValidatingWebhookConfigurationList) []*v1beta1.ValidatingWebhookConfiguration {
				return gentype.ToPointerSlice(list.Items)
			},
			func(list *v1beta1.ValidatingWebhookConfigurationList, items []*v1beta1.ValidatingWebhookConfiguration) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}