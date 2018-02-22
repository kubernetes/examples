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

import static org.hamcrest.Matchers.empty;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.not;
import static org.junit.Assert.assertThat;

import java.net.InetAddress;
import java.util.HashMap;
import java.util.List;

import org.apache.cassandra.locator.SeedProvider;
import org.junit.Ignore;
import org.junit.Test;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

public class KubernetesSeedProviderTest {

	private static final Logger logger = LoggerFactory.getLogger(KubernetesSeedProviderTest.class);

	@Test
	@Ignore("has to be run inside of a kube cluster")
	public void getSeeds() throws Exception {
		SeedProvider provider = new KubernetesSeedProvider(new HashMap<String, String>());
		List<InetAddress> seeds = provider.getSeeds();

		assertThat(seeds, is(not(empty())));

	}
}