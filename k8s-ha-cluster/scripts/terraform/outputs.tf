output "node_info" {
  value = {
    name       = var.node_name
    ip_address = hyperv_machine_instance.vm.network_adaptors[0].ip_addresses[0]
    vm_id      = hyperv_machine_instance.vm.id
  }
  description = "Informasi lengkap node"
}