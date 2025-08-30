
/*
 * Copyright (C) 2015 Google Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not
 * use this file except in compliance with the License. You may obtain a copy of
 * the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
 * License for the specific language governing permissions and limitations under
 * the License.
 */

package io.k8s.cassandra;

import java.io.IOException;
import java.net.InetAddress;
import java.util.Collections;
import java.util.List;
import java.util.Map;

import org.apache.cassandra.locator.SeedProvider;
import org.codehaus.jackson.annotate.JsonIgnoreProperties;
import org.codehaus.jackson.map.ObjectMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import com.sun.jna.Native;

/**
 * Self discovery {@link SeedProvider} that creates a list of Cassandra Seeds by
 * communicating with the Kubernetes API.
 * <p>
 * Various System Variable can be used to configure this provider:
 * <ul>
 * <li>CASSANDRA_SERVICE defaults to cassandra</li>
 * <li>POD_NAMESPACE defaults to 'default'</li>
 * <li>CASSANDRA_SERVICE_NUM_SEEDS defaults to 8 seeds</li>
 * </ul>
 */
public class KubernetesSeedProvider implements SeedProvider {

	private static final Logger logger = LoggerFactory.getLogger(KubernetesSeedProvider.class);


	/**
	 * Create new seed provider
	 *
	 * @param params
	 */
	public KubernetesSeedProvider(Map<String, String> params) {
	}

	/**
	 * Call Kubernetes API to collect a list of seed providers
	 *
	 * @return list of seed providers
	 */
	public List<InetAddress> getSeeds() {
		GoInterface go = (GoInterface) Native.loadLibrary("cassandra-seed.so", GoInterface.class);

		String service = getEnvOrDefault("CASSANDRA_SERVICE", "cassandra");
		String namespace = getEnvOrDefault("POD_NAMESPACE", "default");

		String initialSeeds = getEnvOrDefault("CASSANDRA_SEEDS", "");

		if ("".equals(initialSeeds)) {
			initialSeeds = getEnvOrDefault("POD_IP", "");
		}

		String seedSizeVar = getEnvOrDefault("CASSANDRA_SERVICE_NUM_SEEDS", "8");
		Integer seedSize = Integer.valueOf(seedSizeVar);

		String data = go.GetEndpoints(namespace, service, initialSeeds);
		ObjectMapper mapper = new ObjectMapper();

		try {
			Endpoints endpoints = mapper.readValue(data, Endpoints.class);
			logger.info("cassandra seeds: {}", endpoints.ips.toString());
			return Collections.unmodifiableList(endpoints.ips);
		} catch (IOException e) {
			// This should not happen
			logger.error("unexpected error building cassandra seeds: {}" , e.getMessage());
			return Collections.emptyList();
		}
	}

	private static String getEnvOrDefault(String var, String def) {
		String val = System.getenv(var);
		if (val == null) {
			val = def;
		}
		return val;
	}

	@JsonIgnoreProperties(ignoreUnknown = true)
	static class Endpoints {
		public List<InetAddress> ips;
	}
}
