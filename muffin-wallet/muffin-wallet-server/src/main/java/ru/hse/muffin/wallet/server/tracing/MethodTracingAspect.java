package ru.hse.muffin.wallet.server.tracing;

import io.micrometer.observation.Observation;
import io.micrometer.observation.ObservationRegistry;
import lombok.RequiredArgsConstructor;
import org.aspectj.lang.ProceedingJoinPoint;
import org.aspectj.lang.annotation.Around;
import org.aspectj.lang.annotation.Aspect;
import org.springframework.stereotype.Component;

@Aspect
@Component
@RequiredArgsConstructor
public class MethodTracingAspect {

    private final ObservationRegistry observationRegistry;

    @Around("within(ru.hse.muffin.wallet.server.service..*)")
    public Object traceServiceMethods(ProceedingJoinPoint pjp) throws Throwable {
        return observe(pjp, "wallet.service");
    }

    @Around("within(ru.hse.muffin.wallet.data..*)")
    public Object traceDataMethods(ProceedingJoinPoint pjp) throws Throwable {
        return observe(pjp, "wallet.data");
    }

    private Object observe(ProceedingJoinPoint pjp, String prefix) throws Throwable {
        String cls = pjp.getSignature().getDeclaringType().getSimpleName();
        String method = pjp.getSignature().getName();
        String spanName = prefix + "." + cls + "." + method;

        Observation obs = Observation.createNotStarted(spanName, observationRegistry)
                .lowCardinalityKeyValue("class", cls)
                .lowCardinalityKeyValue("method", method)
                .start();

        try {
            return pjp.proceed();
        } catch (Throwable t) {
            obs.error(t);
            throw t;
        } finally {
            obs.stop();
        }
    }
}
