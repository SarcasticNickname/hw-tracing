package ru.hse.muffin.wallet.server.config;

import io.micrometer.tracing.Tracer;
import org.springframework.boot.web.servlet.FilterRegistrationBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.Ordered;
import ru.hse.muffin.wallet.server.tracing.HttpAccessLogFilter;

@Configuration
public class HttpLoggingConfig {

    @Bean
    public FilterRegistrationBean<HttpAccessLogFilter> httpAccessLogFilter(Tracer tracer) {
        FilterRegistrationBean<HttpAccessLogFilter> reg = new FilterRegistrationBean<>();
        reg.setFilter(new HttpAccessLogFilter(tracer));
        reg.setOrder(Ordered.LOWEST_PRECEDENCE);
        reg.addUrlPatterns("/*");
        return reg;
    }
}
