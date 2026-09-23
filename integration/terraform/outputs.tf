# Files the cincservererlang tests read, written with mode 0600 into .out/
# (gitignored). target.json is the only interface between this stack and the
# tests.

resource "local_sensitive_file" "admin_key" {
  filename        = "${path.module}/.out/admin.pem"
  content         = tls_private_key.admin.private_key_pem
  file_permission = "0600"
}

resource "local_sensitive_file" "ca_cert" {
  filename        = "${path.module}/.out/ca.pem"
  content         = tls_self_signed_cert.ca.cert_pem
  file_permission = "0600"
}

resource "local_sensitive_file" "target" {
  filename        = "${path.module}/.out/target.json"
  file_permission = "0600"
  content = jsonencode({
    server_url     = "https://${aws_eip.server.public_ip}"
    org            = var.org
    admin          = var.admin
    key_path       = abspath(local_sensitive_file.admin_key.filename)
    ca_cert_path   = abspath(local_sensitive_file.ca_cert.filename)
    stats_user     = "statsuser"
    stats_password = random_password.stats.result
  })

  depends_on = [aws_eip_association.server]
}

output "server_url" {
  description = "The CINC Server's URL."
  value       = "https://${aws_eip.server.public_ip}"
}

output "instance_id" {
  description = "For debugging: aws ssm start-session --target <instance_id>."
  value       = aws_instance.server.id
}

output "target_file" {
  description = "Path the cincservererlang tests read."
  value       = abspath(local_sensitive_file.target.filename)
}
