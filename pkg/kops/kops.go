package kops

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/kubicorn/kubicorn/pkg/logger"
	"github.com/pkg/errors"
	"github.com/weaveworks/eksctl/pkg/eks/api"
	"k8s.io/kops/pkg/resources/aws"
	"k8s.io/kops/upup/pkg/fi/cloudup/awsup"
)

// Wrapper for interacting with a kops cluster
type Wrapper struct {
	clusterName string
	cloud       awsup.AWSCloud
}

// NewWrapper constructs a kops wrapper for a given EKS cluster config and kops cluster
func NewWrapper(region, kopsClusterName string) (*Wrapper, error) {
	cloud, err := awsup.NewAWSCloud(region, nil)
	if err != nil {
		return nil, err
	}

	return &Wrapper{kopsClusterName, cloud}, nil
}

func (k *Wrapper) isOwned(t *ec2.Tag) bool {
	return *t.Key == "kubernetes.io/cluster/"+k.clusterName && *t.Value == "owned"
}

func (k *Wrapper) topologyOf(s *ec2.Subnet) api.SubnetTopology {
	for _, t := range s.Tags {
		if *t.Key == "SubnetType" && *t.Value == "Private" {
			return api.SubnetTopologyPrivate
		}
	}
	return api.SubnetTopologyPublic // "Utility", "Public" or unspecified
}

// UseVPC finds VPC and subnets that give kops cluster uses and add those to EKS cluster config
func (k *Wrapper) UseVPC(spec *api.ClusterConfig) error {
	allVPCs, err := aws.ListVPCs(k.cloud, k.clusterName)
	if err != nil {
		return err
	}

	allSubnets, err := aws.ListSubnets(k.cloud, k.clusterName)
	if err != nil {
		return err
	}

	vpcs := []string{}
	for _, vpc := range allVPCs {
		vpc := vpc.Obj.(*ec2.Vpc)
		for _, tag := range vpc.Tags {
			if k.isOwned(tag) {
				vpcs = append(vpcs, *vpc.VpcId)
			}
		}
	}
	logger.Debug("vpcs = %#v", vpcs)
	if len(vpcs) > 1 {
		return fmt.Errorf("more then one VPC found for kops cluster %q", k.clusterName)
	}
	spec.VPC.ID = vpcs[0]

	for _, subnet := range allSubnets {
		subnet := subnet.Obj.(*ec2.Subnet)
		for _, tag := range subnet.Tags {
			if k.isOwned(tag) && *subnet.VpcId == spec.VPC.ID {
				spec.ImportSubnet(k.topologyOf(subnet), *subnet.AvailabilityZone, *subnet.SubnetId)
				spec.AppendAvailabilityZone(*subnet.AvailabilityZone)
			}
		}
	}
	logger.Debug("subnets = %#v", spec.VPC.Subnets)
	if err := spec.HasSufficientSubnets(); err != nil {
		return errors.Wrapf(err, "using VPC from kops cluster %q", k.clusterName)
	}
	return nil
}
