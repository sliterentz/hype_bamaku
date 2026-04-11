terraform {
  required_providers {
    hyperv = {
      source  = "taliesins/hyperv"
      version = "~> 1.0"
    }
  }
}

provider "hyperv" {
  # Konfigurasi provider Hyper-V
}

variable "node_name" {
  type        = string
  description = "Nama node VM"
}

variable "cpus" {
  type        = number
  description = "Jumlah CPU cores"
  default     = 4
}

variable "memory" {
  type        = number
  description = "Memory dalam GB"
  default     = 8
}

variable "disk_size" {
  type        = number
  description = "Disk size dalam GB"
  default     = 50
}

variable "network_switch" {
  type        = string
  description = "Hyper-V Virtual Switch"
  default     = "DefaultSwitch"
}

resource "hyperv_machine_instance" "vm" {
  name               = var.node_name
  generation         = 2
  processor_count    = var.cpus
  dynamic_memory     = true
  memory_startup_bytes = var.memory * 1024 * 1024 * 1024
  
  network_adaptors {
    name        = "eth0"
    switch_name = var.network_switch
  }

  hard_disk_drives {
    controller_type     = "Scsi"
    controller_number   = 0
    controller_location = 0
    path                = "C:\\VMs\\${var.node_name}\\disk.vhdx"
    disk_size_gb        = var.disk_size
  }

  vm_firmware {
    enable_secure_boot = "Off"
  }
}

output "ip_address" {
  value       = hyperv_machine_instance.vm.network_adaptors[0].ip_addresses[0]
  description = "IP Address dari VM"
}

output "vm_id" {
  value       = hyperv_machine_instance.vm.id
  description = "ID VM Hyper-V"
}