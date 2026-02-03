{{- define "muffin-wallet.name" -}}
muffin-wallet
{{- end -}}

{{- define "muffin-wallet.fullname" -}}
{{- printf "%s" (include "muffin-wallet.name" .) -}}
{{- end -}}

{{- define "muffin-wallet.labels" -}}
app: {{ include "muffin-wallet.name" . }}
{{- end -}}
