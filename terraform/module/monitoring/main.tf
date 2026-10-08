# ---------------------------------------------------------------------------
# kube-prometheus-stack: Prometheus Operator, Prometheus, Alertmanager,
# Grafana, node-exporter and kube-state-metrics in one Helm release.
# Prometheus and Grafana keep their data on gp3 EBS volumes (StatefulSets).
# ---------------------------------------------------------------------------

locals {
  grafana_host    = "grafana.${var.domain_name}"
  prometheus_host = "prometheus.${var.domain_name}"

  # Same ALB as the StatusBoard Ingresses (IngressGroup)
  alb_annotations = {
    "alb.ingress.kubernetes.io/scheme"          = "internet-facing"
    "alb.ingress.kubernetes.io/target-type"     = "ip"
    "alb.ingress.kubernetes.io/group.name"      = var.alb_group_name
    "alb.ingress.kubernetes.io/listen-ports"    = "[{\"HTTP\": 80}, {\"HTTPS\": 443}]"
    "alb.ingress.kubernetes.io/ssl-redirect"    = "443"
    "alb.ingress.kubernetes.io/certificate-arn" = var.certificate_arn
    "alb.ingress.kubernetes.io/success-codes"   = "200-399"
  }

  values = {
    crds = { enabled = true }

    # EKS runs the control plane for us: these components cannot be scraped,
    # so disable them to avoid permanent "target down" alerts.
    kubeEtcd              = { enabled = false }
    kubeControllerManager = { enabled = false }
    kubeScheduler         = { enabled = false }
    kubeProxy             = { enabled = false }

    grafana = {
      defaultDashboardsTimezone = "browser"
      ingress = {
        enabled          = true
        ingressClassName = "alb"
        annotations = merge(local.alb_annotations, {
          "alb.ingress.kubernetes.io/healthcheck-path" = "/api/health"
        })
        hosts = [local.grafana_host]
        path  = "/"
        tls   = [{ hosts = [local.grafana_host] }]
      }
      # Grafana as a StatefulSet with its own EBS volume
      persistence = {
        enabled          = true
        type             = "sts"
        storageClassName = var.storage_class_name
        accessModes      = ["ReadWriteOnce"]
        size             = "10Gi"
      }
      sidecar = {
        dashboards = {
          enabled         = true
          label           = "grafana_dashboard"
          labelValue      = "1"
          searchNamespace = "ALL" # picks up the StatusBoard dashboard ConfigMap
        }
      }
    }

    prometheus = {
      ingress = {
        enabled          = var.expose_prometheus
        ingressClassName = "alb"
        annotations = merge(local.alb_annotations, {
          "alb.ingress.kubernetes.io/healthcheck-path" = "/-/healthy"
        })
        hosts    = [local.prometheus_host]
        paths    = ["/"]
        pathType = "Prefix"
        tls      = [{ hosts = [local.prometheus_host] }]
      }
      prometheusSpec = {
        retention = var.prometheus_retention
        # Discover ServiceMonitors / PrometheusRules from ALL Helm releases,
        # not only from this one (needed for the StatusBoard chart)
        serviceMonitorSelectorNilUsesHelmValues = false
        podMonitorSelectorNilUsesHelmValues     = false
        ruleSelectorNilUsesHelmValues           = false
        storageSpec = {
          volumeClaimTemplate = {
            spec = {
              storageClassName = var.storage_class_name
              accessModes      = ["ReadWriteOnce"]
              resources        = { requests = { storage = var.prometheus_storage_size } }
            }
          }
        }
        resources = {
          requests = { cpu = "250m", memory = "1Gi" }
          limits   = { memory = "2Gi" }
        }
      }
    }

    alertmanager = {
      alertmanagerSpec = {
        storage = {
          volumeClaimTemplate = {
            spec = {
              storageClassName = var.storage_class_name
              accessModes      = ["ReadWriteOnce"]
              resources        = { requests = { storage = "2Gi" } }
            }
          }
        }
      }
    }
  }
}

resource "helm_release" "kube_prometheus_stack" {
  name             = "kube-prometheus-stack"
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  version          = var.chart_version
  namespace        = "monitoring"
  create_namespace = true
  timeout          = 900

  values = [yamlencode(local.values)]

  # Kept out of the values file so it never appears in plan output
  set_sensitive = [{
    name  = "grafana.adminPassword"
    value = var.grafana_admin_password
  }]
}
