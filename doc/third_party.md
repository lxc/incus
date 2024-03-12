# Third party tools and integrations
Below are a list of common operations tools which feature Incus support, either natively or through a plugin.

## Terraform / OpenTofu
[Terraform](https://www.terraform.io) and [OpenTofu](https://opentofu.org) are infrastructure as code tools which focus on creating the infrastructure itself.
For Incus, this means the ability to create projects, profiles, networks, storage volumes and of course instances.

In most cases, one will then use Ansible to deploy the workloads
themselves once the instances and everything else they need as been put in place.

The integration with Incus is done through a [dedicated provider](https://github.com/lxc/terraform-provider-incus).

## Ansible
[Ansible](https://www.ansible.com) is an infrastructure as code tool with particular focus on software provisioning and configuration management.
It does most of its work by first connecting to the system that it's deploying software on.

To do that, it can connect over SSH and a variety of other protocols, one of which is [Incus](https://docs.ansible.com/ansible/latest/collections/community/general/incus_connection.html).

That allows for easily deploying software inside of Incus instances without needing to first setup SSH.

## Packer
[Packer](https://www.packer.io) is a tool to generate custom OS images across a wide variety of platforms.

A [plugin](https://developer.hashicorp.com/packer/integrations/bketelsen/incus) exists that allows Packer to generate Incus images directly.

## Distrobuilder
[Distrobuilder](https://github.com/lxc/distrobuilder) is an image building tool most known for producing the official LXC and Incus images.
It consumes YAML definitions for its images and generates LXC container images as well as Incus container and VM images.

The focus of Distrobuilder is in producing clean images from scratch, as opposed to repacking existing images.

## GARM
[GARM](https://github.com/cloudbase/garm) is the Github Actions Runner Manager which allows for running self-hosted Github runners.

It supports a variety of providers for those runners, including [Incus](https://github.com/cloudbase/garm-provider-incus).
