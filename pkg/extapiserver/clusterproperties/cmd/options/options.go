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
	"fmt"
	"net"
	"strings"

	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericapiserveropts "k8s.io/apiserver/pkg/server/options"
	utilcompatibility "k8s.io/apiserver/pkg/util/compatibility"
	"k8s.io/client-go/pkg/version"
	"k8s.io/component-base/cli/flag"
	"k8s.io/component-base/compatibility"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"

	clusterpropertiesv1beta1 "github.com/kubefleet-dev/kubefleet/apis/clusterproperties/v1beta1"
	"github.com/kubefleet-dev/kubefleet/pkg/extapiserver/clusterproperties/pkg/server"
	generatedopenapi "github.com/kubefleet-dev/kubefleet/pkg/generated/clusterproperties/openapi"
)

type Options struct {
	// Generic API server options from the apiserver package.
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

	// Disable authentication and authorization in the extension API server.
	//
	// This flag should not be raisen in a production environment.
	DisableAuthnAuthz bool
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

// AddFlags adds flags related to the KubeFleet cluster properties extension API server to the
// specified flag set.
func (o *Options) AddFlags(fs flag.NamedFlagSets) {
	// TO-DO (chenyu1): prepare a cluster properties extension API server specific flag set.

	// Add the generic API server flags.
	o.GenericServerRunOpts.AddUniversalFlags(fs.FlagSet("generic server run"))
	o.GenericSecureServingOpts.AddFlags(fs.FlagSet("generic secure serving"))
	o.GenericAuthnOpts.AddFlags(fs.FlagSet("generic authentication"))
	o.GenericAuthzOpts.AddFlags(fs.FlagSet("generic authorization"))
	o.GenericAuditOpts.AddFlags(fs.FlagSet("generic auditing"))
	o.GenericFeatureOpts.AddFlags(fs.FlagSet("generic feature set"))
	logsapi.AddFlags(o.GenericLoggingOpts, fs.FlagSet("generic logging"))
}

// NewWithDefaultValues creates a new Options struct with default values as set in a generic
// extension API server.
func NewWithDefaultValues() *Options {
	return &Options{
		// TO-DO (chenyu1): evaluate if the default values in use are appropriate for the cluster
		// properties extension API server, and adjust as needed.
		GenericServerRunOpts: genericapiserveropts.NewServerRunOptionsForComponent(
			"cluster properties extension API server",
			compatibility.NewComponentGlobalsRegistry()),
		GenericSecureServingOpts: genericapiserveropts.NewSecureServingOptions().WithLoopback(),
		GenericAuthnOpts:         genericapiserveropts.NewDelegatingAuthenticationOptions(),
		GenericAuthzOpts:         genericapiserveropts.NewDelegatingAuthorizationOptions(),
		GenericFeatureOpts:       genericapiserveropts.NewFeatureOptions(),
		GenericAuditOpts:         genericapiserveropts.NewAuditOptions(),
		GenericLoggingOpts:       logs.NewOptions(),
	}
}

func (o *Options) BuildServer(
	hostname string,
	alternateDNSNames []string,
	alternateIPs []net.IP,
) (*server.Server, error) {
	genericAPIServerCfg, err := o.BuildGenericAPIServerCfg(hostname, alternateDNSNames, alternateIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare generic API server configuration: %w", err)
	}

	server, err := server.New(genericAPIServerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build extension API server: %w", err)
	}
	return server, nil
}

func (o *Options) BuildGenericAPIServerCfg(
	hostname string,
	alternateDNSNames []string,
	alternateIPs []net.IP,
) (*genericapiserver.Config, error) {
	// Prepare the self-signed certificates (if applicable).
	if err := o.GenericSecureServingOpts.MaybeDefaultWithSelfSignedCerts(hostname, alternateDNSNames, alternateIPs); err != nil {
		return nil, fmt.Errorf("failed to prepare self-signed certificates: %w", err)
	}

	// Set the global component variable registry.
	//
	// Note that the registry is currently unused in the KubeFleet cluster properties extension API server.
	if err := o.GenericServerRunOpts.ComponentGlobalsRegistry.Set(); err != nil {
		return nil, fmt.Errorf("failed to set the global component variable registry: %w", err)
	}

	genericAPIServerCfg := genericapiserver.NewConfig(clusterpropertiesv1beta1.CodecFactory)
	// Apply the generic server run options.
	if err := o.GenericServerRunOpts.ApplyTo(genericAPIServerCfg); err != nil {
		return nil, fmt.Errorf("failed to apply generic server run options: %w", err)
	}

	// Apply the secure serving options.
	if err := o.GenericSecureServingOpts.ApplyTo(
		&genericAPIServerCfg.SecureServing,
		&genericAPIServerCfg.LoopbackClientConfig,
	); err != nil {
		return nil, fmt.Errorf("failed to apply generic secure serving options: %w", err)
	}

	// Apply the authentication and authorization options, if not disabled.
	if !o.DisableAuthnAuthz {
		if err := o.GenericAuthnOpts.ApplyTo(
			&genericAPIServerCfg.Authentication,
			genericAPIServerCfg.SecureServing,
			nil,
		); err != nil {
			return nil, fmt.Errorf("failed to apply generic authentication options: %w", err)
		}

		if err := o.GenericAuthzOpts.ApplyTo(&genericAPIServerCfg.Authorization); err != nil {
			return nil, fmt.Errorf("failed to apply generic authorization options: %w", err)
		}
	}

	// Apply the audit options.
	if err := o.GenericAuditOpts.ApplyTo(genericAPIServerCfg); err != nil {
		return nil, fmt.Errorf("failed to apply generic audit options: %w", err)
	}

	// Set up OpenAPI schemas.
	verInfo := version.Get()

	genericAPIServerCfg.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(
		generatedopenapi.GetOpenAPIDefinitions,
		openapinamer.NewDefinitionNamer(clusterpropertiesv1beta1.Scheme),
	)
	genericAPIServerCfg.OpenAPIConfig.Info.Title = "KubeFleet Cluster Properties Extension API Server"
	genericAPIServerCfg.OpenAPIConfig.Info.Version = strings.Split(verInfo.String(), "-")[0]

	genericAPIServerCfg.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		generatedopenapi.GetOpenAPIDefinitions,
		openapinamer.NewDefinitionNamer(clusterpropertiesv1beta1.Scheme),
	)
	genericAPIServerCfg.OpenAPIV3Config.Info.Title = "KubeFleet Cluster Properties Extension API Server"
	genericAPIServerCfg.OpenAPIV3Config.Info.Version = strings.Split(verInfo.String(), "-")[0]

	genericAPIServerCfg.EffectiveVersion = utilcompatibility.DefaultBuildEffectiveVersion()

	return genericAPIServerCfg, nil
}

func Run(hostname string, alternateDNS []string, alternateIPs []net.IP, o *Options, stopCh <-chan struct{}) error {
	errs := o.Validate()
	if len(errs) > 0 {
		return fmt.Errorf("invalid options: %v", errs)
	}

	// Build the server.
	server, err := o.BuildServer(hostname, alternateDNS, alternateIPs)
	if err != nil {
		return fmt.Errorf("failed to build server: %w", err)
	}

	return server.RunUntil(stopCh)
}
