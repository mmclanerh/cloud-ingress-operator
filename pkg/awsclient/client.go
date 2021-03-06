package awsclient

// TODO: Retry upon API failure

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/route53"

	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elb/elbiface"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclientpkg "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsCredsSecretIDKey     = "aws_access_key_id"
	awsCredsSecretAccessKey = "aws_secret_access_key"
)

// NewAwsClientInput input for new aws client
type NewAwsClientInput struct {
	AwsCredsSecretIDKey     string
	AwsCredsSecretAccessKey string
	AwsToken                string
	AwsRegion               string
	SecretName              string
	NameSpace               string
}

// Client wraps for AWS SDK (for easier testing)
type Client interface {
	/*
	 * ELB-related Functions
	 */

	/*
	 * ELBv2-related Functions
	 */

	// list all or 1 NLB to get external or internal
	DescribeLoadBalancersV2(*elbv2.DescribeLoadBalancersInput) (*elbv2.DescribeLoadBalancersOutput, error)
	// delete external NLB so we can make cluster private
	DeleteLoadBalancerV2(*elbv2.DeleteLoadBalancerInput) (*elbv2.DeleteLoadBalancerOutput, error)
	// create nlb to make server api public
	CreateLoadBalancerV2(*elbv2.CreateLoadBalancerInput) (*elbv2.CreateLoadBalancerOutput, error)
	// create targetGroup for new external NLB
	CreateTargetGroupV2(*elbv2.CreateTargetGroupInput) (*elbv2.CreateTargetGroupOutput, error)
	// register master instances with target group
	RegisterTargetsV2(*elbv2.RegisterTargetsInput) (*elbv2.RegisterTargetsOutput, error)
	// create listener for an NLB
	CreateListenerV2(*elbv2.CreateListenerInput) (*elbv2.CreateListenerOutput, error)
	// describes the targetGroup for NLB
	DescribeTargetGroupsV2(*elbv2.DescribeTargetGroupsInput) (*elbv2.DescribeTargetGroupsOutput, error)
	// add tags for an NLB
	AddTagsV2(*elbv2.AddTagsInput) (*elbv2.AddTagsOutput, error)

	/*
	 * Route 53-related Functions
	 */

	// Route 53 - to update DNS for internal/external swap and to add rh-api
	// for actually upserting the record
	ChangeResourceRecordSets(*route53.ChangeResourceRecordSetsInput) (*route53.ChangeResourceRecordSetsOutput, error)
	// to turn baseDomain into a Route53 zone ID
	ListHostedZonesByName(*route53.ListHostedZonesByNameInput) (*route53.ListHostedZonesByNameOutput, error)

	/*
	 * EC2-related Functions
	 */
	// DescribeSubnets to find subnet for master nodes for incoming elb
	DescribeSubnets(*ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error)

	// Helper extensions
	// ec2
	SubnetNameToSubnetIDLookup([]string) ([]string, error)

	// elb/elbv2
	DoesELBExist(string) (bool, *AWSLoadBalancer, error)
	ListAllNLBs() ([]LoadBalancerV2, error)
	DeleteExternalLoadBalancer(string) error
	CreateNetworkLoadBalancer(string, string, string) ([]LoadBalancerV2, error)
	CreateListenerForNLB(string, string) error
	GetTargetGroupArn(string) (string, error)

	// route53
	UpsertARecord(string, string, string, string, string, bool) error
}

type AwsClient struct {
	ec2Client     ec2iface.EC2API
	route53Client route53iface.Route53API
	elbClient     elbiface.ELBAPI
	elbv2Client   elbv2iface.ELBV2API
}

func NewClient(accessID, accessSecret, token, region string) (*AwsClient, error) {
	awsConfig := &aws.Config{Region: aws.String(region)}
	if token == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", accessID)
		os.Setenv("AWS_SECRET_ACCESS_KEY", accessSecret)
	} else {
		awsConfig.Credentials = credentials.NewStaticCredentials(accessID, accessSecret, token)
	}
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}
	return &AwsClient{
		ec2Client:     ec2.New(s),
		elbClient:     elb.New(s),
		elbv2Client:   elbv2.New(s),
		route53Client: route53.New(s),
	}, nil
}

// GetAWSClient generates an awsclient
// function must include region
// Pass in token if sessions requires a token
// if it includes a secretName and nameSpace it will create credentials from that secret data
// If it includes awsCredsSecretIDKey and awsCredsSecretAccessKey it will build credentials from those
func GetAWSClient(kubeClient kubeclientpkg.Client, input NewAwsClientInput) (*AwsClient, error) {

	// error if region is not included
	if input.AwsRegion == "" {
		return nil, fmt.Errorf("getAWSClient:NoRegion: %v", input.AwsRegion)
	}

	if input.SecretName != "" && input.NameSpace != "" {
		secret := &corev1.Secret{}
		err := kubeClient.Get(context.TODO(),
			types.NamespacedName{
				Name:      input.SecretName,
				Namespace: input.NameSpace,
			},
			secret)
		if err != nil {
			return nil, err
		}
		accessKeyID, ok := secret.Data[awsCredsSecretIDKey]
		if !ok {
			return nil, fmt.Errorf("AWS credentials secret %v did not contain key %v",
				input.SecretName, awsCredsSecretIDKey)
		}
		secretAccessKey, ok := secret.Data[awsCredsSecretAccessKey]
		if !ok {
			return nil, fmt.Errorf("AWS credentials secret %v did not contain key %v",
				input.SecretName, awsCredsSecretAccessKey)
		}

		AwsClient, err := NewClient(string(accessKeyID), string(secretAccessKey), input.AwsToken, input.AwsRegion)
		if err != nil {
			return nil, err
		}
		return AwsClient, nil
	}

	if input.AwsCredsSecretIDKey == "" && input.AwsCredsSecretAccessKey != "" {
		return nil, fmt.Errorf("getAWSClient: NoAwsCredentials or Secret %v", input)
	}

	AwsClient, err := NewClient(input.AwsCredsSecretIDKey, input.AwsCredsSecretAccessKey, input.AwsToken, input.AwsRegion)
	if err != nil {
		return nil, err
	}
	return AwsClient, nil
}

func (c *AwsClient) ApplySecurityGroupsToLoadBalancer(i *elb.ApplySecurityGroupsToLoadBalancerInput) (*elb.ApplySecurityGroupsToLoadBalancerOutput, error) {
	return c.elbClient.ApplySecurityGroupsToLoadBalancer(i)
}

func (c *AwsClient) ConfigureHealthCheck(i *elb.ConfigureHealthCheckInput) (*elb.ConfigureHealthCheckOutput, error) {
	return c.elbClient.ConfigureHealthCheck(i)
}

func (c *AwsClient) CreateLoadBalancer(i *elb.CreateLoadBalancerInput) (*elb.CreateLoadBalancerOutput, error) {
	return c.elbClient.CreateLoadBalancer(i)
}

func (c *AwsClient) CreateLoadBalancerListeners(i *elb.CreateLoadBalancerListenersInput) (*elb.CreateLoadBalancerListenersOutput, error) {
	return c.elbClient.CreateLoadBalancerListeners(i)
}

func (c *AwsClient) DeleteLoadBalancerListeners(i *elb.DeleteLoadBalancerListenersInput) (*elb.DeleteLoadBalancerListenersOutput, error) {
	return c.elbClient.DeleteLoadBalancerListeners(i)
}

func (c *AwsClient) DeregisterInstancesFromLoadBalancer(i *elb.DeregisterInstancesFromLoadBalancerInput) (*elb.DeregisterInstancesFromLoadBalancerOutput, error) {
	return c.elbClient.DeregisterInstancesFromLoadBalancer(i)
}
func (c *AwsClient) DescribeLoadBalancers(i *elb.DescribeLoadBalancersInput) (*elb.DescribeLoadBalancersOutput, error) {
	return c.elbClient.DescribeLoadBalancers(i)
}

func (c *AwsClient) DescribeLoadBalancersV2(i *elbv2.DescribeLoadBalancersInput) (*elbv2.DescribeLoadBalancersOutput, error) {
	return c.elbv2Client.DescribeLoadBalancers(i)
}

func (c *AwsClient) DeleteLoadBalancerV2(i *elbv2.DeleteLoadBalancerInput) (*elbv2.DeleteLoadBalancerOutput, error) {
	return c.elbv2Client.DeleteLoadBalancer(i)
}

func (c *AwsClient) CreateLoadBalancerV2(i *elbv2.CreateLoadBalancerInput) (*elbv2.CreateLoadBalancerOutput, error) {
	return c.elbv2Client.CreateLoadBalancer(i)
}

func (c *AwsClient) CreateTargetGroupV2(i *elbv2.CreateTargetGroupInput) (*elbv2.CreateTargetGroupOutput, error) {
	return c.elbv2Client.CreateTargetGroup(i)
}

func (c *AwsClient) RegisterTargetsV2(i *elbv2.RegisterTargetsInput) (*elbv2.RegisterTargetsOutput, error) {
	return c.elbv2Client.RegisterTargets(i)
}

func (c *AwsClient) CreateListenerV2(i *elbv2.CreateListenerInput) (*elbv2.CreateListenerOutput, error) {
	return c.elbv2Client.CreateListener(i)
}

func (c *AwsClient) DescribeTags(i *elb.DescribeTagsInput) (*elb.DescribeTagsOutput, error) {
	return c.elbClient.DescribeTags(i)
}
func (c *AwsClient) RegisterInstancesWithLoadBalancer(i *elb.RegisterInstancesWithLoadBalancerInput) (*elb.RegisterInstancesWithLoadBalancerOutput, error) {
	return c.elbClient.RegisterInstancesWithLoadBalancer(i)
}

func (c *AwsClient) DescribeTargetGroupsV2(i *elbv2.DescribeTargetGroupsInput) (*elbv2.DescribeTargetGroupsOutput, error) {
	return c.elbv2Client.DescribeTargetGroups(i)
}

func (c *AwsClient) AddTagsV2(i *elbv2.AddTagsInput) (*elbv2.AddTagsOutput, error) {
	return c.elbv2Client.AddTags(i)
}

func (c *AwsClient) ChangeResourceRecordSets(i *route53.ChangeResourceRecordSetsInput) (*route53.ChangeResourceRecordSetsOutput, error) {
	return c.route53Client.ChangeResourceRecordSets(i)
}
func (c *AwsClient) ListHostedZonesByName(i *route53.ListHostedZonesByNameInput) (*route53.ListHostedZonesByNameOutput, error) {
	return c.route53Client.ListHostedZonesByName(i)
}

func (c *AwsClient) AuthorizeSecurityGroupIngress(i *ec2.AuthorizeSecurityGroupIngressInput) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return c.ec2Client.AuthorizeSecurityGroupIngress(i)
}
func (c *AwsClient) CreateSecurityGroup(i *ec2.CreateSecurityGroupInput) (*ec2.CreateSecurityGroupOutput, error) {
	return c.ec2Client.CreateSecurityGroup(i)
}
func (c *AwsClient) DeleteSecurityGroup(i *ec2.DeleteSecurityGroupInput) (*ec2.DeleteSecurityGroupOutput, error) {
	return c.ec2Client.DeleteSecurityGroup(i)
}
func (c *AwsClient) DescribeSecurityGroups(i *ec2.DescribeSecurityGroupsInput) (*ec2.DescribeSecurityGroupsOutput, error) {
	return c.ec2Client.DescribeSecurityGroups(i)
}
func (c *AwsClient) RevokeSecurityGroupIngress(i *ec2.RevokeSecurityGroupIngressInput) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return c.ec2Client.RevokeSecurityGroupIngress(i)
}
func (c *AwsClient) DescribeSubnets(i *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
	return c.ec2Client.DescribeSubnets(i)
}
func (c *AwsClient) CreateTags(i *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	return c.ec2Client.CreateTags(i)
}
