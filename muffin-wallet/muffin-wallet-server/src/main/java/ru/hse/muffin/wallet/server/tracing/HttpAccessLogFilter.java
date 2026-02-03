package ru.hse.muffin.wallet.server.tracing;

import io.micrometer.tracing.Span;
import io.micrometer.tracing.Tracer;
import jakarta.annotation.PostConstruct;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.web.filter.OncePerRequestFilter;

import java.io.IOException;

import static net.logstash.logback.argument.StructuredArguments.kv;

public class HttpAccessLogFilter extends OncePerRequestFilter {

    private static final Logger log = LoggerFactory.getLogger("http.access");
    private static final String TRACE_HEADER = "X-Trace-Id";

    private final Tracer tracer;

    public HttpAccessLogFilter(Tracer tracer) {
        this.tracer = tracer;
    }

    @PostConstruct
    public void init() {
        log.info("HttpAccessLogFilter initialized");
    }

    @Override
    protected boolean shouldNotFilter(HttpServletRequest request) {
        String p = request.getRequestURI();
        return p.startsWith("/actuator") || p.startsWith("/swagger-ui") || p.startsWith("/v3/api-docs");
    }

    @Override
    protected void doFilterInternal(
            HttpServletRequest request,
            HttpServletResponse response,
            FilterChain filterChain
    ) throws ServletException, IOException {

        long startNs = System.nanoTime();

        log.info("Request started",
                kv("method", request.getMethod()),
                kv("path", request.getRequestURI())
        );

        try {
            filterChain.doFilter(request, response);
        } finally {
            long durMs = (System.nanoTime() - startNs) / 1_000_000;

            Span span = tracer.currentSpan();
            if (span != null) {
                response.setHeader(TRACE_HEADER, span.context().traceId());
            }

            log.info("Request completed",
                    kv("method", request.getMethod()),
                    kv("path", request.getRequestURI()),
                    kv("status", response.getStatus()),
                    kv("duration_ms", durMs)
            );
        }
    }
}
