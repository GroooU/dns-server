terraform {
  required_providers {
    yandex = {
      source  = "yandex-cloud/yandex"
      version = "~> 0.140"
    }
  }
  required_version = ">= 1.5"
}

provider "yandex" {
  token     = var.yc_token
  cloud_id  = var.yc_cloud_id
  folder_id = var.yc_folder_id
  zone      = var.yc_zone
}

# ── Network ─────────────────────────────────────────────────────────────────

resource "yandex_vpc_network" "dns_net" {
  name = "dns-forwarder-net"
}

resource "yandex_vpc_subnet" "dns_subnet" {
  name           = "dns-forwarder-subnet"
  zone           = var.yc_zone
  network_id     = yandex_vpc_network.dns_net.id
  v4_cidr_blocks = ["10.10.0.0/24"]
}

# ── Security Group ───────────────────────────────────────────────────────────

resource "yandex_vpc_security_group" "dns_sg" {
  name       = "dns-forwarder-sg"
  network_id = yandex_vpc_network.dns_net.id

  # Allow SSH
  ingress {
    protocol       = "TCP"
    port           = 22
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow DNS over UDP
  ingress {
    protocol       = "UDP"
    port           = 53
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow DNS over TCP
  ingress {
    protocol       = "TCP"
    port           = 53
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow Prometheus metrics / health
  ingress {
    protocol       = "TCP"
    port           = 8053
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow Grafana UI
  ingress {
    protocol       = "TCP"
    port           = 3000
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow Prometheus UI
  ingress {
    protocol       = "TCP"
    port           = 9090
    v4_cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow all egress (needed to reach upstream DoT/DoH resolvers)
  egress {
    protocol       = "ANY"
    from_port      = 0
    to_port        = 65535
    v4_cidr_blocks = ["0.0.0.0/0"]
  }
}

# ── Static IP ────────────────────────────────────────────────────────────────

resource "yandex_vpc_address" "dns_ip" {
  name = "dns-forwarder-ip"

  external_ipv4_address {
    zone_id = var.yc_zone
  }
}

# ── Container Registry ───────────────────────────────────────────────────────

resource "yandex_container_registry" "dns_registry" {
  name      = "dns-forwarder"
  folder_id = var.yc_folder_id
}

# ── Compute VM ───────────────────────────────────────────────────────────────

data "yandex_compute_image" "ubuntu_2204" {
  family = "ubuntu-2204-lts"
}

resource "yandex_compute_instance" "dns_vm" {
  name        = "dns-forwarder"
  platform_id = "standard-v3"
  zone        = var.yc_zone

  resources {
    cores         = 2
    memory        = 2
    core_fraction = 100
  }

  boot_disk {
    initialize_params {
      image_id = data.yandex_compute_image.ubuntu_2204.id
      size     = 20
      type     = "network-ssd"
    }
  }

  network_interface {
    subnet_id          = yandex_vpc_subnet.dns_subnet.id
    security_group_ids = [yandex_vpc_security_group.dns_sg.id]
    nat                = true
    nat_ip_address     = yandex_vpc_address.dns_ip.external_ipv4_address[0].address
  }

  metadata = {
    ssh-keys  = "ubuntu:${var.ssh_public_key}"
    user-data = <<-EOT
      #cloud-config
      package_update: true
      packages:
        - apt-transport-https
        - ca-certificates
        - curl
        - gnupg
        - lsb-release

      runcmd:
        # Install Docker
        - curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
        - echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" > /etc/apt/sources.list.d/docker.list
        - apt-get update -y
        - apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
        - systemctl enable docker
        - systemctl start docker
        # Create app directory
        - mkdir -p /app
    EOT
  }

  scheduling_policy {
    preemptible = false
  }
}
