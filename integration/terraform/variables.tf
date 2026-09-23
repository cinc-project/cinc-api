variable "region" {
  description = "AWS region for the test server."
  type        = string
  default     = "us-west-2"
}

variable "instance_type" {
  description = "EC2 instance type. The CINC Server Erlang stack wants 8 GB or more of memory; 4 vCPUs keep `reconfigure` fast."
  type        = string
  default     = "t3.xlarge"
}

variable "cinc_server_version" {
  description = "CINC Server version to install, from the stable channel."
  type        = string
  default     = "15.10.125"
}

variable "allowed_cidr" {
  description = "CIDR allowed to reach the server on 443. Empty means the caller's current public IP as a /32."
  type        = string
  default     = ""
}

variable "name_prefix" {
  description = "Prefix for resource names and the Name tag."
  type        = string
  default     = "cinc-api-it"
}

variable "org" {
  description = "Organization the tests use."
  type        = string
  default     = "cincapi"
}

variable "admin" {
  description = "Admin user the tests authenticate as. It is a member of org and a server-admin."
  type        = string
  default     = "cincapi-admin"
}
