{{/* Chart name */}}
{{- define "statusboard.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fully qualified app name, e.g. "statusboard" for release "statusboard" */}}
{{- define "statusboard.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/* Common labels */}}
{{- define "statusboard.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "statusboard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: statusboard
statusboard/environment: {{ .Values.environment | quote }}
{{- end }}

{{/* Selector labels for one component (app, postgres, valkey or backup) */}}
{{- define "statusboard.selectorLabels" -}}
app.kubernetes.io/name: {{ include "statusboard.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/* Resource names */}}
{{/* The PostgreSQL cluster. The operator also names its primary Service after it. */}}
{{- define "statusboard.postgresName" -}}
{{- printf "%s-db" (include "statusboard.fullname" .) }}
{{- end }}

{{/*
Host for read-only work (backups, exports): the replica Service when there are
replicas, so heavy reads never slow down the primary; otherwise the primary.
*/}}
{{- define "statusboard.postgresReadHost" -}}
{{- if gt (int .Values.postgres.instances) 1 -}}
{{- printf "%s-repl" (include "statusboard.postgresName" .) }}
{{- else -}}
{{- include "statusboard.postgresName" . }}
{{- end -}}
{{- end }}

{{/* Secrets created by the postgres-operator: {username}.{cluster}.credentials.postgresql.acid.zalan.do */}}
{{- define "statusboard.postgresUserSecret" -}}
{{- printf "%s.%s.credentials.postgresql.acid.zalan.do" .Values.postgres.user (include "statusboard.postgresName" .) }}
{{- end }}

{{- define "statusboard.postgresSuperuserSecret" -}}
{{- printf "postgres.%s.credentials.postgresql.acid.zalan.do" (include "statusboard.postgresName" .) }}
{{- end }}

{{/*
Folder name in the backup and export buckets: the environment key Terraform uses
(namespace statusboard-prod -> "prod"). The IAM roles only allow writing there.
*/}}
{{- define "statusboard.envFolder" -}}
{{- trimPrefix "statusboard-" .Release.Namespace }}
{{- end }}

{{- define "statusboard.exportServiceAccount" -}}
statusboard-export
{{- end }}

{{- define "statusboard.valkeyName" -}}
{{- printf "%s-valkey" (include "statusboard.fullname" .) }}
{{- end }}

{{- define "statusboard.secretName" -}}
{{- printf "%s-secrets" (include "statusboard.fullname" .) }}
{{- end }}

{{- define "statusboard.backupServiceAccount" -}}
{{- printf "%s-backup" (include "statusboard.fullname" .) }}
{{- end }}

{{/*
Hardened container security context, compatible with the "restricted" Pod Security Standard.
Usage: include "statusboard.containerSecurity" (dict "readOnly" true)
*/}}
{{- define "statusboard.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: {{ .readOnly }}
capabilities:
  drop: ["ALL"]
{{- end }}

{{/*
Return a base64 value for a Secret key:
  1. the value from values.yaml if provided
  2. otherwise the value already stored in the cluster (so upgrades never rotate passwords)
  3. otherwise a new random string
Usage: include "statusboard.secretValue" (list $existingSecret "key" .Values.auth.x 32)
*/}}
{{- define "statusboard.secretValue" -}}
{{- $existing := index . 0 -}}
{{- $key := index . 1 -}}
{{- $override := index . 2 -}}
{{- $length := index . 3 -}}
{{- if $override -}}
{{- $override | b64enc -}}
{{- else if and $existing (hasKey $existing.data $key) -}}
{{- index $existing.data $key -}}
{{- else -}}
{{- randAlphaNum $length | b64enc -}}
{{- end -}}
{{- end }}
