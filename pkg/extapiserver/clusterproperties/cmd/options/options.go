/*
Copyright 2026 The KubeFleet Authors.

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

package options

import (
	genericapiserveropts "k8s.io/apiserver/pkg/server/options"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
)

type Options struct {
	// Common API server options from the apiserver package.
	//
	// Not all options available for a generic API server are exposed here;
	// for a list of all available options, refer to genericapiserver.RecommendedConfig and its
	// child/embedded fields.
	//
	// Note that among the most commonly used options, etcd-related options are not included,
	// as this extension API server uses an in-memory store instead.
	GenericServerRunOpts     *genericapiserveropts.ServerRunOptions
	GenericSecureServingOpts *genericapiserveropts.SecureServingOptionsWithLoopback
	GenericAuthnOpts         *genericapiserveropts.DelegatingAuthenticationOptions
	GenericAuthzOpts         *genericapiserveropts.DelegatingAuthorizationOptions
	GenericAuditOpts         *genericapiserveropts.AuditOptions
	GenericFeatureOpts       *genericapiserveropts.FeatureOptions
	GenericLoggingOpts       *logs.Options

	// KubeFleet cluster properties extension API server specific options.
	// TO-DO (chenyu1): add more options as needed.
}

// Validate validates an Options struct.
func (o *Options) Validate() []error {
	errs := make([]error, 0)
	// Validate the apply logging options.
	if err := logsapi.ValidateAndApply(o.GenericLoggingOpts, nil); err != nil {
		errs = append(errs, err)
	}

	// Validate the ServerRun options.
	if svrRunErrs := o.GenericServerRunOpts.Validate(); len(svrRunErrs) > 0 {
		errs = append(errs, svrRunErrs...)
	}

	// Validate the SecureServing options.
	if secureServingErrs := o.GenericSecureServingOpts.Validate(); len(secureServingErrs) > 0 {
		errs = append(errs, secureServingErrs...)
	}

	// Validate the Authn and Authz options.
	if authnErrs := o.GenericAuthnOpts.Validate(); len(authnErrs) > 0 {
		errs = append(errs, authnErrs...)
	}
	if authzErrs := o.GenericAuthzOpts.Validate(); len(authzErrs) > 0 {
		errs = append(errs, authzErrs...)
	}

	// Validate the Audit options.
	if auditErrs := o.GenericAuditOpts.Validate(); len(auditErrs) > 0 {
		errs = append(errs, auditErrs...)
	}

	// Validate the Feature options.
	if featureErrs := o.GenericFeatureOpts.Validate(); len(featureErrs) > 0 {
		errs = append(errs, featureErrs...)
	}
	return errs
}
