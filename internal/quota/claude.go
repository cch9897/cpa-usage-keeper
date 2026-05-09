package quota

import (
	"context"

	"cpa-usage-keeper/internal/cpa/dto/apicall"
)

type claudeProvider struct {
	caller        ManagementAPICaller
	usageConfig   APICallConfig
	profileConfig APICallConfig
}

func NewClaudeProvider(caller ManagementAPICaller, usageConfig APICallConfig, profileConfig APICallConfig) ProviderHandler {
	return claudeProvider{caller: caller, usageConfig: usageConfig, profileConfig: profileConfig}
}

func (p claudeProvider) Check(ctx context.Context, input ProviderInput) (ProviderOutput, error) {
	usageResponse, err := p.caller.CallManagementAPI(ctx, apicall.Request{
		AuthIndex: input.Identity.Identity,
		Method:    p.usageConfig.Method,
		URL:       p.usageConfig.URL,
		Header:    p.usageConfig.Headers,
	})
	if err != nil {
		return ProviderOutput{}, err
	}
	usage, err := parseClaudeUsagePayload(usageResponse)
	if err != nil {
		return ProviderOutput{}, err
	}
	profileResponse, err := p.caller.CallManagementAPI(ctx, apicall.Request{
		AuthIndex: input.Identity.Identity,
		Method:    p.profileConfig.Method,
		URL:       p.profileConfig.URL,
		Header:    p.profileConfig.Headers,
	})
	if err != nil {
		return ProviderOutput{}, err
	}
	profile, err := parseClaudeProfilePayload(profileResponse)
	if err != nil {
		return ProviderOutput{}, err
	}
	return ProviderOutput{Provider: "claude", Result: ClaudeResult{Usage: usage, Profile: profile}}, nil
}
