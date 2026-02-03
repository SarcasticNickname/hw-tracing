{{- define "muffin-currency.name" -}}
muffin-currency
{{- end -}}

{{- define "muffin-currency.fullname" -}}
{{- printf "%s" (include "muffin-currency.name" .) -}}
{{- end -}}

{{- define "muffin-currency.labels" -}}
app: {{ include "muffin-currency.name" . }}
{{- end -}}
