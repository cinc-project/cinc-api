# The caller's public IP, for the default security-group rule.
data "http" "caller_ip" {
  count = var.allowed_cidr == "" ? 1 : 0
  url   = "https://checkip.amazonaws.com"
}

# The package to install, pinned by SHA-256 at apply time.
data "http" "cinc_server_package" {
  url             = "https://omnitruck.cinc.sh/stable/cinc-server/metadata?p=ubuntu&pv=22.04&m=x86_64&v=${var.cinc_server_version}"
  request_headers = { Accept = "application/json" }
}

data "aws_ssm_parameter" "ubuntu_ami" {
  name = "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  allowed_cidr = var.allowed_cidr != "" ? var.allowed_cidr : "${chomp(data.http.caller_ip[0].response_body)}/32"
  package      = jsondecode(data.http.cinc_server_package.response_body)
}

# --- network: a small VPC of its own, so the stack works in any account and
# destroys cleanly.

resource "aws_vpc" "this" {
  cidr_block           = "10.42.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
}

resource "aws_subnet" "public" {
  vpc_id            = aws_vpc.this.id
  cidr_block        = "10.42.1.0/24"
  availability_zone = data.aws_availability_zones.available.names[0]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

# HTTPS from the caller only. No SSH: debug through SSM Session Manager.
resource "aws_security_group" "server" {
  name_prefix = "${var.name_prefix}-"
  description = "CINC Server for cinc-api integration tests"
  vpc_id      = aws_vpc.this.id
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.server.id
  description       = "HTTPS from the test runner"
  cidr_ipv4         = local.allowed_cidr
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.server.id
  description       = "Package download and SSM"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# --- SSM access for debugging (cloud-init log, cinc-server-ctl).

resource "aws_iam_role" "server" {
  name_prefix = "${var.name_prefix}-"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.server.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "server" {
  name_prefix = "${var.name_prefix}-"
  role        = aws_iam_role.server.name
}

# --- the server. The Elastic IP exists before the instance so the TLS
# certificate can name it.

resource "aws_eip" "server" {
  domain = "vpc"
}

resource "aws_instance" "server" {
  ami                                  = data.aws_ssm_parameter.ubuntu_ami.value
  instance_type                        = var.instance_type
  subnet_id                            = aws_subnet.public.id
  vpc_security_group_ids               = [aws_security_group.server.id]
  iam_instance_profile                 = aws_iam_instance_profile.server.name
  associate_public_ip_address          = true
  instance_initiated_shutdown_behavior = "terminate"

  metadata_options {
    http_tokens = "required"
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = 50
    encrypted   = true
  }

  user_data_replace_on_change = true
  user_data = templatefile("${path.module}/bootstrap.sh.tftpl", {
    package_url      = local.package.url
    package_sha256   = local.package.sha256
    api_fqdn         = aws_eip.server.public_ip
    server_cert_pem  = tls_locally_signed_cert.server.cert_pem
    server_key_pem   = tls_private_key.server.private_key_pem
    admin            = var.admin
    admin_public_key = tls_private_key.admin.public_key_pem
    org              = var.org
    stats_password   = random_password.stats.result
  })

  depends_on = [aws_route_table_association.public, aws_internet_gateway.this]
}

resource "aws_eip_association" "server" {
  instance_id   = aws_instance.server.id
  allocation_id = aws_eip.server.id
}
