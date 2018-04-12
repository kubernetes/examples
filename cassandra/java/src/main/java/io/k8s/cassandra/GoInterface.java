
/*
 * Copyright (C) 2018 Google Inc.
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

import java.util.Arrays;
import java.util.List;

import com.sun.jna.Library;
import com.sun.jna.Pointer;
import com.sun.jna.Structure;

public interface GoInterface extends Library {
	public String GetEndpoints(String namespace, String service, String seeds);

	public class GoSlice extends Structure {
		public static class ByValue extends GoSlice implements Structure.ByValue {
		}

		public Pointer data;
		public long len;
		public long cap;

		protected List<String> getFieldOrder() {
			return Arrays.asList(new String[] { "data", "len", "cap" });
		}
	}

	public class GoString extends Structure {
		public static class ByValue extends GoString implements Structure.ByValue {
		}

		public String p;
		public long n;

		protected List<String> getFieldOrder() {
			return Arrays.asList(new String[] { "p", "n" });
		}
	}
}