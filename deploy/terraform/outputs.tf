output "vm_external_ip" {
  description = "Static external IPv4 address of the DNS forwarder VM"
  value       = yandex_vpc_address.dns_ip.external_ipv4_address[0].address
}

output "registry_id" {
  description = "Yandex Container Registry ID"
  value       = yandex_container_registry.dns_registry.id
}

output "registry_endpoint" {
  description = "Container Registry push endpoint"
  value       = "cr.yandex/${yandex_container_registry.dns_registry.id}"
}
